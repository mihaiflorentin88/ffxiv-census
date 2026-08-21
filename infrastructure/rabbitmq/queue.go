package rabbitmq

import (
	"context"
	"errors"
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
	url     string
	conn    *amqp.Connection
	ch      *amqp.Channel
	returns <-chan amqp.Return
	mu      sync.Mutex
	logger  contract.Logger
}

// openSession dials the broker, opens a channel, declares the full topology,
// enables publisher confirms, and registers a return listener. On any setup
// error the partially opened channel/connection are closed and the error is
// returned without modifying the queue state.
func (q *Queue) openSession() (*amqp.Connection, *amqp.Channel, <-chan amqp.Return, error) {
	conn, err := amqp.Dial(q.url)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("rabbitmq dial: %w", err)
	}

	ch, err := conn.Channel()
	if err != nil {
		conn.Close()
		return nil, nil, nil, fmt.Errorf("rabbitmq channel: %w", err)
	}

	if err := q.declareTopology(ch); err != nil {
		ch.Close()
		conn.Close()
		return nil, nil, nil, fmt.Errorf("rabbitmq topology: %w", err)
	}

	if err := ch.Confirm(false); err != nil {
		ch.Close()
		conn.Close()
		return nil, nil, nil, fmt.Errorf("rabbitmq confirm: %w", err)
	}

	rets := ch.NotifyReturn(make(chan amqp.Return, 1))

	return conn, ch, rets, nil
}

// New creates a new RabbitMQ Queue. It dials the broker, declares the full
// topology (exchanges, queues, bindings), enables publisher confirms, and
// returns a ready-to-use Queue.
func New(url string, logger contract.Logger) (*Queue, error) {
	q := &Queue{url: url, logger: logger}
	conn, ch, rets, err := q.openSession()
	if err != nil {
		return nil, err
	}
	q.conn = conn
	q.ch = ch
	q.returns = rets
	return q, nil
}

// declareTopology idempotently declares all exchanges, queues, and bindings.
func (q *Queue) declareTopology(ch *amqp.Channel) error {
	if err := ch.ExchangeDeclare(exchangeMain, "direct", true, false, false, false, nil); err != nil {
		return fmt.Errorf("declare exchange %s: %w", exchangeMain, err)
	}

	for _, et := range eventTypes() {
		mainQueue := "census." + et
		failedQueue := mainQueue + ".failed"

		if _, err := ch.QueueDeclare(mainQueue, true, false, false, false, nil); err != nil {
			return fmt.Errorf("declare queue %s: %w", mainQueue, err)
		}
		if err := ch.QueueBind(mainQueue, et, exchangeMain, false, nil); err != nil {
			return fmt.Errorf("bind queue %s: %w", mainQueue, err)
		}

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

// Publish sends a single job to the main exchange with routing key = job.Type
// and waits for broker confirmation. A successful return means the durable
// target queue accepted the message.
func (q *Queue) Publish(ctx context.Context, job contract.QueueJob) error {
	q.mu.Lock()
	defer q.mu.Unlock()

	if q.conn.IsClosed() {
		if err := q.reconnect(); err != nil {
			return fmt.Errorf("publish reconnect: %w", err)
		}
	}

	// Drain any stale return notification from the previous publish.
	select {
	case <-q.returns:
	default:
	}

	dc, err := q.ch.PublishWithDeferredConfirmWithContext(ctx, exchangeMain, job.Type, true, false, amqp.Publishing{
		ContentType:  "application/json",
		DeliveryMode: amqp.Persistent,
		Body:         job.Payload,
		Headers:      amqp.Table{headerAttempts: int32(0)},
	})
	if err != nil {
		return fmt.Errorf("publish: %w", err)
	}
	if dc == nil {
		return fmt.Errorf("publish: deferred confirmation is nil")
	}

	acked, err := dc.WaitContext(ctx)
	if err != nil {
		return fmt.Errorf("publish confirm: %w", err)
	}
	if !acked {
		return fmt.Errorf("publish: broker nacked message for %q", job.Type)
	}

	// RabbitMQ notifies returns before confirming mandatory messages.
	select {
	case ret := <-q.returns:
		return fmt.Errorf("rabbitmq unroutable: code=%d text=%q exchange=%q routing_key=%q", ret.ReplyCode, ret.ReplyText, ret.Exchange, ret.RoutingKey)
	default:
	}

	return nil
}

// Consume starts concurrency consumers for the given event types.
func (q *Queue) Consume(ctx context.Context, eventTypes []string, concurrency int, handler func(ctx context.Context, job contract.QueueJob) error) error {
	if concurrency <= 0 {
		concurrency = 4
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
// messages back to the main exchange.
func (q *Queue) ConsumeFailed(ctx context.Context, types []string, concurrency int) error {
	if concurrency <= 0 {
		concurrency = 4
	}

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
			q.logger.WarnContext(
				processCtx, "rabbitmq.failed.permanent_discard",
				slog.String("event_type", eventType),
				slog.Int("attempts", attempts),
			)
			_ = msg.Ack(false)
			continue
		}

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
		if err := q.publishToFailed(failedQueue, eventType, msg.Body, msg.Headers, attempts, backoff); err != nil {
			q.logger.ErrorContext(ctx, "rabbitmq.retry_publish_error", slog.Any("error", err))
		}
	}

	_ = msg.Ack(false)
}

// publishToFailed publishes to the per-event-type failed queue.
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

// Close closes the publishing channel and then the AMQP connection.
func (q *Queue) Close() error {
	q.mu.Lock()
	defer q.mu.Unlock()
	var chErr, connErr error
	if q.ch != nil {
		chErr = q.ch.Close()
	}
	if q.conn != nil && !q.conn.IsClosed() {
		connErr = q.conn.Close()
	}
	return errors.Join(chErr, connErr)
}

// reconnect re-establishes the AMQP connection, channel, and confirm/return handling.
func (q *Queue) reconnect() error {
	conn, ch, rets, err := q.openSession()
	if err != nil {
		return err
	}
	q.conn = conn
	q.ch = ch
	q.returns = rets
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
			for {
				select {
				case msg, ok := <-c:
					if !ok {
						return
					}
					select {
					case merged <- msg:
					case <-ctx.Done():
						return
					}
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
