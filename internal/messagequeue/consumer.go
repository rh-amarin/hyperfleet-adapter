package messagequeue

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"time"

	"github.com/lib/pq"

	"github.com/openshift-hyperfleet/hyperfleet-adapter/pkg/logger"
)

const defaultPollInterval = 10 * time.Second
const defaultWorkers = 5
const defaultBatchSize = 10

type Message struct {
	ID          string
	AdapterName string
	Kind        string
	ResourceID  string
	Payload     []byte
}

type HandlerFunc func(ctx context.Context, payload []byte) error

type ConsumerConfig struct {
	AdapterName  string
	PollInterval time.Duration
	Workers      int
	BatchSize    int
}

type Consumer struct {
	db      *sql.DB
	connStr string
	cfg     ConsumerConfig
	handler HandlerFunc
	log     logger.Logger
}

func NewConsumer(db *sql.DB, connStr string, cfg ConsumerConfig, handler HandlerFunc, log logger.Logger) *Consumer {
	if cfg.PollInterval == 0 {
		cfg.PollInterval = defaultPollInterval
	}
	if cfg.Workers == 0 {
		cfg.Workers = defaultWorkers
	}
	if cfg.BatchSize == 0 {
		cfg.BatchSize = defaultBatchSize
	}
	return &Consumer{
		db:      db,
		connStr: connStr,
		cfg:     cfg,
		handler: handler,
		log:     log,
	}
}

func (c *Consumer) Start(ctx context.Context) {
	c.log.Infof(ctx, "message consumer starting (adapter=%s, workers=%d, poll=%s)",
		c.cfg.AdapterName, c.cfg.Workers, c.cfg.PollInterval)

	work := make(chan Message, c.cfg.Workers)

	var wg sync.WaitGroup
	for i := range c.cfg.Workers {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			c.worker(ctx, workerID, work)
		}(i)
	}

	notify := c.startListener(ctx)

	ticker := time.NewTicker(c.cfg.PollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			c.log.Info(ctx, "message consumer shutting down")
			close(work)
			wg.Wait()
			c.log.Info(ctx, "message consumer stopped")
			return
		case <-notify:
			c.claimAndDispatch(ctx, work)
		case <-ticker.C:
			c.claimAndDispatch(ctx, work)
		}
	}
}

func (c *Consumer) startListener(ctx context.Context) <-chan struct{} {
	notify := make(chan struct{}, 1)

	plog := func(ev pq.ListenerEventType, err error) {
		if err != nil {
			c.log.Errorf(logger.WithErrorField(ctx, err), "pg listener error")
		}
	}

	listener := pq.NewListener(c.connStr, 10*time.Second, time.Minute, plog)
	if err := listener.Listen("messages"); err != nil {
		c.log.Errorf(logger.WithErrorField(ctx, err),
			"failed to LISTEN on messages channel, falling back to polling only")
		return notify
	}

	go func() {
		for {
			select {
			case <-ctx.Done():
				listener.Close() //nolint:errcheck
				return
			case n := <-listener.Notify:
				if n == nil {
					continue
				}
				select {
				case notify <- struct{}{}:
				default:
				}
			}
		}
	}()

	c.log.Info(ctx, "pg LISTEN on 'messages' channel established")
	return notify
}

func (c *Consumer) claimAndDispatch(ctx context.Context, work chan<- Message) {
	messages, err := c.claimBatch(ctx)
	if err != nil {
		c.log.Errorf(logger.WithErrorField(ctx, err), "failed to claim messages")
		return
	}
	for _, msg := range messages {
		select {
		case work <- msg:
		case <-ctx.Done():
			return
		}
	}
}

func (c *Consumer) claimBatch(ctx context.Context) ([]Message, error) {
	rows, err := c.db.QueryContext(ctx,
		`UPDATE messages
		 SET status = 'claimed', claimed_at = NOW()
		 WHERE id IN (
		     SELECT id FROM messages
		     WHERE adapter_name = $1 AND status = 'pending'
		     ORDER BY created_at
		     LIMIT $2
		     FOR UPDATE SKIP LOCKED
		 )
		 RETURNING id, adapter_name, kind, resource_id, payload`,
		c.cfg.AdapterName, c.cfg.BatchSize,
	)
	if err != nil {
		return nil, fmt.Errorf("claim query: %w", err)
	}
	defer rows.Close() //nolint:errcheck

	var messages []Message
	for rows.Next() {
		var msg Message
		if err := rows.Scan(&msg.ID, &msg.AdapterName, &msg.Kind, &msg.ResourceID, &msg.Payload); err != nil {
			return nil, fmt.Errorf("scan message: %w", err)
		}
		messages = append(messages, msg)
	}
	return messages, rows.Err()
}

func (c *Consumer) worker(ctx context.Context, id int, work <-chan Message) {
	for msg := range work {
		msgCtx := logger.WithLogField(ctx, "message_id", msg.ID)
		msgCtx = logger.WithLogField(msgCtx, "resource_id", msg.ResourceID)

		err := c.handler(msgCtx, msg.Payload)
		if err != nil {
			c.markFailed(msgCtx, msg.ID, err.Error())
		} else {
			c.markCompleted(msgCtx, msg.ID)
		}
	}
}

func (c *Consumer) markCompleted(ctx context.Context, messageID string) {
	_, err := c.db.ExecContext(ctx,
		`UPDATE messages SET status = 'completed', completed_at = NOW() WHERE id = $1`,
		messageID,
	)
	if err != nil {
		c.log.Errorf(logger.WithErrorField(ctx, err), "failed to mark message completed")
	}
}

func (c *Consumer) markFailed(ctx context.Context, messageID string, errMsg string) {
	_, err := c.db.ExecContext(ctx,
		`UPDATE messages SET status = 'failed', completed_at = NOW(), error_message = $2 WHERE id = $1`,
		messageID, errMsg,
	)
	if err != nil {
		c.log.Errorf(logger.WithErrorField(ctx, err), "failed to mark message failed")
	}
}
