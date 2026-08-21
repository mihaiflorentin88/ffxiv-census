package rabbitmq

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"sync"

	amqp "github.com/rabbitmq/amqp091-go"

	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

const (
	exchangeMain      = "census"
	headerAttempts    = "x-attempts"
	maxAttempts       = 5
	maxFailedAttempts = 100
	backoffBaseSec    = 5
	maxBackoffSec     = 3600
)

// Queue is a RabbitMQ-backed work queue implementing contract.Queue.
type Queue struct {
	url    string
	conn   *amqp.Connection
	ch     *amqp.Channel
	mu     sync.Mutex
	logger contract.Logger
}

// New creates a new RabbitMQ Queue. It dials the broker, declares the full
// topology (exchanges, queues, bindings), and returns a ready-to-use Queue.
func New(url string, logger contract.Logger) (*Queue, error) {
	conn, err := amqp.Dial(url)
	if err != nil {
		return nil, fmt.Errorf("rabbitmq dial: %w", err)
	}

	ch, err := conn.Channel()
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("rabbitmq channel: %w", err)
	}

	q := &Queue{url: url, conn: conn, ch: ch, logger: logger}
	if err := q.declareTopology(ch); err != nil {
		q.Close()
		return nil, fmt.Errorf("rabbitmq topology: %w", err)
	}

	return q, nil
}

// declareTopology idempotently declares all exchanges, queues, and bindings.
//
// Per event type (e.g. "id-sweep"):
//
//	census (exchange, direct)
//	  └─ routing key "id-sweep" → census.id-sweep (queue)
//
//	census.id-sweep.failed (queue)
//	  └─ dead-letters back to census exchange, routing key "id-sweep"
//	  └─ retry messages have TTL (auto-retry), permanent failures have no TTL (stay forever)
func (q *Queue) declareTopology(ch *amqp.Channel) error {
	if err := ch.ExchangeDeclare(exchangeMain, "direct", true, false, false, false, nil); err != nil {
		return fmt.Errorf("declare exchange %s: %w", exchangeMain, err)
	}

	for _, et := range eventTypes() {
		mainQueue := "census." + et
		failedQueue := mainQueue + ".failed"

		// Main queue.
		if _, err := ch.QueueDeclare(mainQueue, true, false, false, false, nil); err != nil {
			return fmt.Errorf("declare queue %s: %w", mainQueue, err)
		}
		if err := ch.QueueBind(mainQueue, et, exchangeMain, false, nil); err != nil {
			return fmt.Errorf("bind queue %s: %w", mainQueue, err)
		}

		// Failed queue — dead-letters back to main queue for retries.
		failedArgs := amqp.Table{
			"x-dead-letter-exchange":    exchangeMain,
			"x-dead-letter-routing-key": et,
		}
		if _, err := ch.QueueDeclare(failedQueue, true, false, false, false, failedArgs); err != nil {
			return fmt.Errorf("declare queue %s: %w", failedQueue, err)
		}
	}

	return nil
}

// Publish sends a single job to the main exchange with routing key = job.Type.
func (q *Queue) Publish(ctx context.Context, job contract.QueueJob) error {
	q.mu.Lock()
	defer q.mu.Unlock()

	if q.conn.IsClosed() {
		if err := q.reconnect(); err != nil {
			return fmt.Errorf("publish reconnect: %w", err)
		}
	}

	return q.ch.PublishWithContext(ctx, exchangeMain, job.Type, false, false, amqp.Publishing{
		ContentType:  "application/json",
		DeliveryMode: amqp.Persistent,
		Body:         job.Payload,
		Headers:      amqp.Table{headerAttempts: int32(0)},
	})
}

// Consume starts concurrency consumers for the given event types. Each message
// is dispatched to handler. On handler return:
//   - nil → ack
//   - error → retry with exponential backoff; after maxAttempts → dead-letter
//
// Consume blocks until ctx is cancelled. On cancellation, it stops accepting
// new messages but allows in-flight handlers to finish before returning.
func (q *Queue) Consume(ctx context.Context, eventTypes []string, concurrency int, handler func(ctx context.Context, job contract.QueueJob) error) error {
	if concurrency <= 0 {
		concurrency = 4
	}

	// stopClaiming is cancelled when the signal arrives, causing workers to
	// stop accepting new messages. processCtx stays alive so in-flight
	// handlers can finish their work.
	stopClaiming, stopClaimingCancel := context.WithCancel(context.Background())
	defer stopClaimingCancel()

	// Watch for the signal context to cancel stopClaiming.
	go func() {
		<-ctx.Done()
		stopClaimingCancel()
	}()

	// processCtx is used for in-flight handler calls — it is NOT cancelled
	// by the signal, so handlers can complete gracefully.
	processCtx, processCancel := context.WithCancel(context.Background())
	defer processCancel()

	var wg sync.WaitGroup
	errCh := make(chan error, concurrency)

	for i := range concurrency {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			if err := q.consumeWorker(stopClaiming, processCtx, eventTypes, workerID, handler); err != nil && stopClaiming.Err() == nil {
				errCh <- err
			}
		}(i)
	}

	wg.Wait()
	close(errCh)

	var errs []error
	for err := range errCh {
		if err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("consume errors: %v", errs)
	}
	return nil
}

// ConsumeFailed consumes from per-event-type failed queues and re-publishes
// messages back to the main exchange. Each message's attempt count is incremented.
// Messages that have exceeded maxFailedAttempts (100) are permanently discarded.
// If eventTypes is empty, consumes from all failed queues.
func (q *Queue) ConsumeFailed(ctx context.Context, types []string, concurrency int) error {
	if concurrency <= 0 {
		concurrency = 4
	}

	// Build list of failed queue names.
	var failedQueues []string
	if len(types) == 0 {
		for _, et := range eventTypes() {
			failedQueues = append(failedQueues, "census."+et+".failed")
		}
	} else {
		for _, et := range types {
			failedQueues = append(failedQueues, "census."+et+".failed")
		}
	}

	stopClaiming, stopClaimingCancel := context.WithCancel(context.Background())
	defer stopClaimingCancel()

	go func() {
		<-ctx.Done()
		stopClaimingCancel()
	}()

	processCtx, processCancel := context.WithCancel(context.Background())
	defer processCancel()

	var wg sync.WaitGroup
	errCh := make(chan error, concurrency)

	for i := range concurrency {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			if err := q.failedWorker(stopClaiming, processCtx, failedQueues, workerID); err != nil && stopClaiming.Err() == nil {
				errCh <- err
			}
		}(i)
	}

	wg.Wait()
	close(errCh)

	var errs []error
	for err := range errCh {
		if err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("failed consumer errors: %v", errs)
	}
	return nil
}

// failedWorker consumes from failed queues and re-publishes to main exchange.
func (q *Queue) failedWorker(stopClaiming context.Context, processCtx context.Context, failedQueues []string, workerID int) error {
	ch, err := q.conn.Channel()
	if err != nil {
		return fmt.Errorf("failed worker %d channel: %w", workerID, err)
	}
	defer ch.Close()

	if err := ch.Qos(1, 0, false); err != nil {
		return fmt.Errorf("failed worker %d qos: %w", workerID, err)
	}

	var deliveries []<-chan amqp.Delivery
	for _, fq := range failedQueues {
		del, err := ch.Consume(fq, "", false, false, false, false, nil)
		if err != nil {
			return fmt.Errorf("failed worker %d consume %s: %w", workerID, fq, err)
		}
		deliveries = append(deliveries, del)
	}

	merged := mergeDeliveries(stopClaiming, deliveries)

	for msg := range merged {
		select {
		case <-stopClaiming.Done():
			_ = msg.Nack(false, true)
			return nil
		default:
		}

		attempts := getAttempts(msg.Headers) + 1
		eventType := msg.RoutingKey

		if attempts >= maxFailedAttempts {
			// Permanently discard — too many failures.
			q.logger.WarnContext(
				processCtx, "rabbitmq.failed.permanent_discard",
				slog.String("event_type", eventType),
				slog.Int("attempts", attempts),
			)
			_ = msg.Ack(false)
			continue
		}

		// Re-publish to main exchange with incremented attempt count.
		newHeaders := copyHeaders(msg.Headers, attempts)
		pubErr := ch.PublishWithContext(processCtx, exchangeMain, eventType, false, false, amqp.Publishing{
			ContentType:  "application/json",
			DeliveryMode: amqp.Persistent,
			Body:         msg.Body,
			Headers:      newHeaders,
		})
		if pubErr != nil {
			q.logger.ErrorContext(
				processCtx, "rabbitmq.failed.republish_error",
				slog.String("event_type", eventType),
				slog.Int("attempts", attempts),
				slog.Any("error", pubErr),
			)
			_ = msg.Nack(false, true)
			continue
		}

		_ = msg.Ack(false)
		q.logger.InfoContext(
			processCtx, "rabbitmq.failed.republished",
			slog.String("event_type", eventType),
			slog.Int("attempts", attempts),
		)
	}

	return nil
}

// consumeWorker creates a dedicated channel and consumes from the specified queues.
// stopClaiming controls when to stop accepting new messages.
// processCtx is passed to handlers so they can finish during graceful shutdown.
func (q *Queue) consumeWorker(stopClaiming context.Context, processCtx context.Context, eventTypes []string, workerID int, handler func(ctx context.Context, job contract.QueueJob) error) error {
	ch, err := q.conn.Channel()
	if err != nil {
		return fmt.Errorf("worker %d channel: %w", workerID, err)
	}
	defer ch.Close()

	if err := ch.Qos(1, 0, false); err != nil {
		return fmt.Errorf("worker %d qos: %w", workerID, err)
	}

	var deliveries []<-chan amqp.Delivery
	for _, et := range eventTypes {
		queueName := "census." + et
		del, err := ch.Consume(queueName, "", false, false, false, false, nil)
		if err != nil {
			return fmt.Errorf("worker %d consume %s: %w", workerID, queueName, err)
		}
		deliveries = append(deliveries, del)
	}

	merged := mergeDeliveries(stopClaiming, deliveries)

	for msg := range merged {
		select {
		case <-stopClaiming.Done():
			_ = msg.Nack(false, true)
			return nil
		default:
		}

		job := contract.QueueJob{
			Type:    msg.RoutingKey,
			Payload: msg.Body,
		}

		err := handler(processCtx, job)
		if err != nil {
			q.handleFailure(processCtx, msg, err)
		} else {
			_ = msg.Ack(false)
		}
	}

	return nil
}

// handleFailure publishes the failed message to the per-event-type failed queue.
// With TTL for retry (auto-dead-letters back to main), without TTL for permanent failure.
func (q *Queue) handleFailure(ctx context.Context, msg amqp.Delivery, handlerErr error) {
	attempts := getAttempts(msg.Headers) + 1
	eventType := msg.RoutingKey
	failedQueue := "census." + eventType + ".failed"

	if attempts >= maxAttempts {
		q.logger.WarnContext(
			ctx, "rabbitmq.permanent_failure",
			slog.String("event_type", eventType),
			slog.Int("attempts", attempts),
			slog.Any("error", handlerErr),
		)
		// No TTL — stays in failed queue permanently.
		if err := q.publishToFailed(failedQueue, eventType, msg.Body, msg.Headers, attempts, 0); err != nil {
			q.logger.ErrorContext(ctx, "rabbitmq.failed_publish_error", slog.Any("error", err))
		}
	} else {
		backoff := backoffBaseSec * int(math.Pow(2, float64(attempts-1)))
		if backoff > maxBackoffSec {
			backoff = maxBackoffSec
		}
		q.logger.WarnContext(
			ctx, "rabbitmq.retry",
			slog.String("event_type", eventType),
			slog.Int("attempts", attempts),
			slog.Int("backoff_sec", backoff),
			slog.Any("error", handlerErr),
		)
		// With TTL — auto-dead-letters back to main queue after backoff.
		if err := q.publishToFailed(failedQueue, eventType, msg.Body, msg.Headers, attempts, backoff); err != nil {
			q.logger.ErrorContext(ctx, "rabbitmq.retry_publish_error", slog.Any("error", err))
		}
	}

	_ = msg.Ack(false)
}

// publishToFailed publishes to the per-event-type failed queue.
// If backoffSec > 0, the message has a TTL and will auto-dead-letter back to main.
// If backoffSec == 0, the message stays permanently.
func (q *Queue) publishToFailed(failedQueue, eventType string, body []byte, headers amqp.Table, attempts, backoffSec int) error {
	newHeaders := copyHeaders(headers, attempts)
	pub := amqp.Publishing{
		ContentType:  "application/json",
		DeliveryMode: amqp.Persistent,
		Body:         body,
		Headers:      newHeaders,
	}
	if backoffSec > 0 {
		pub.Expiration = fmt.Sprintf("%d000", backoffSec)
	}
	return q.ch.PublishWithContext(context.Background(), "", failedQueue, false, false, pub)
}

// Close closes the AMQP connection.
func (q *Queue) Close() error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.conn != nil && !q.conn.IsClosed() {
		return q.conn.Close()
	}
	return nil
}

// reconnect re-establishes the AMQP connection and channel.
func (q *Queue) reconnect() error {
	conn, err := amqp.Dial(q.url)
	if err != nil {
		return fmt.Errorf("rabbitmq dial: %w", err)
	}
	ch, err := conn.Channel()
	if err != nil {
		conn.Close()
		return fmt.Errorf("rabbitmq channel: %w", err)
	}
	q.conn = conn
	q.ch = ch
	return nil
}

// getAttempts extracts the attempt count from message headers.
func getAttempts(headers amqp.Table) int {
	if headers == nil {
		return 0
	}
	if v, ok := headers[headerAttempts]; ok {
		if n, ok := v.(int32); ok {
			return int(n)
		}
	}
	return 0
}

// copyHeaders creates a new headers table with updated attempt count.
func copyHeaders(headers amqp.Table, attempts int) amqp.Table {
	new := amqp.Table{headerAttempts: int32(attempts)}
	if headers != nil {
		for k, v := range headers {
			if k != headerAttempts {
				new[k] = v
			}
		}
	}
	return new
}

// eventTypes returns the known event type strings used for queue naming.
func eventTypes() []string {
	return []string{
		"id-sweep",
		"character-census",
		"achievement-census",
		"new-proxy",
		"scan-proxy",
	}
}

// mergeDeliveries merges multiple amqp.Delivery channels into one.
func mergeDeliveries(ctx context.Context, channels []<-chan amqp.Delivery) <-chan amqp.Delivery {
	merged := make(chan amqp.Delivery)
	var wg sync.WaitGroup

	for _, ch := range channels {
		wg.Add(1)
		go func(c <-chan amqp.Delivery) {
			defer wg.Done()
			for msg := range c {
				select {
				case merged <- msg:
				case <-ctx.Done():
					return
				}
			}
		}(ch)
	}

	go func() {
		wg.Wait()
		close(merged)
	}()

	return merged
}

// Ensure Queue implements contract.Queue at compile time.
var _ contract.Queue = (*Queue)(nil)
