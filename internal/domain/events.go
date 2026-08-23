package domain

import "time"

type AuditEvent struct {
	ID             string
	OrganizationID string
	ActorUserID    string
	RequestID      string
	Action         string
	ObjectType     string
	ObjectID       string
	Outcome        string
	Metadata       string
	OccurredAt     time.Time
}

type OutboxStatus string

const (
	OutboxPending   OutboxStatus = "pending"
	OutboxSending   OutboxStatus = "sending"
	OutboxRetry     OutboxStatus = "retry"
	OutboxDelivered OutboxStatus = "delivered"
	OutboxDead      OutboxStatus = "dead"
)

type OutboxEvent struct {
	ID              string
	OrganizationID  string
	Topic           string
	AggregateType   string
	AggregateID     string
	IdempotencyKey  string
	Payload         []byte
	Status          OutboxStatus
	AttemptCount    int
	MaxAttempts     int
	AvailableAt     time.Time
	LeaseOwner      string
	LeaseToken      string
	LeaseGeneration int64
	LeaseExpiresAt  *time.Time
	LastError       string
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

func (e OutboxEvent) CanRetry() bool { return e.AttemptCount < e.MaxAttempts }

type PageRequest struct {
	Limit  int
	Cursor string
}

func (p PageRequest) Normalized() PageRequest {
	if p.Limit <= 0 {
		p.Limit = 50
	}
	if p.Limit > 200 {
		p.Limit = 200
	}
	return p
}

type Page[T any] struct {
	Items      []T    `json:"items"`
	NextCursor string `json:"next_cursor,omitempty"`
}
