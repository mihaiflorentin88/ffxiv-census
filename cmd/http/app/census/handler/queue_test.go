package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	mockqueue "github.com/mihaiflorentin88/ffxiv-census/mock/queue"
	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
	"github.com/mihaiflorentin88/ffxiv-census/port/dto/response"
)

func TestQueueController_PurgeByStatus(t *testing.T) {
	statuses := []struct {
		name         string
		targetStatus string
	}{
		{"pending", "pending"},
		{"claimed", "claimed"},
		{"done", "done"},
		{"failed", "failed"},
		{"all", "all"},
	}

	for _, tt := range statuses {
		t.Run(tt.name, func(t *testing.T) {
			q := mockqueue.NewFake()
			ctx := context.Background()
			qc := NewQueueController(q)

			_, _ = q.Publish(ctx,
				contract.QueueJob{Type: "id-sweep", Payload: []byte(`{"from":1,"to":100}`)},
				contract.QueueJob{Type: "id-sweep", Payload: []byte(`{"from":101,"to":200}`)},
				contract.QueueJob{Type: "id-sweep", Payload: []byte(`{"from":201,"to":300}`)},
				contract.QueueJob{Type: "id-sweep", Payload: []byte(`{"from":301,"to":400}`)},
			)
			claimed, _ := q.Claim(ctx, "id-sweep", 3, contract.ClaimModeAny)
			_ = q.Complete(ctx, claimed[1].ID)
			_ = q.Fail(ctx, claimed[2].ID, "test failure")

			body, _ := json.Marshal(map[string]string{
				"status":     tt.targetStatus,
				"older_than": "0s",
			})
			req := httptest.NewRequest(http.MethodPost, "/api/v1/queue/purge", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()

			qc.Purge(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("expected status 200, got %d: %s", rec.Code, rec.Body.String())
			}

			var resp response.QueuePurgeResponse
			if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
				t.Fatalf("unmarshal purge response: %v", err)
			}

			expectedPurged := int64(1)
			if tt.targetStatus == "all" {
				expectedPurged = 4
			}
			if resp.Purged != expectedPurged {
				t.Errorf("expected %d purged, got %d", expectedPurged, resp.Purged)
			}
			if resp.Status != tt.targetStatus {
				t.Errorf("expected status %q in response, got %q", tt.targetStatus, resp.Status)
			}
		})
	}
}
