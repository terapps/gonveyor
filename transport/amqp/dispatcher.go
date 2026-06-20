package amqp

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"time"

	amqp091 "github.com/rabbitmq/amqp091-go"
	"github.com/terapps/gonveyor"
	"github.com/terapps/gonveyor/ledger"
	"github.com/terapps/gonveyor/transport"
)

var _ transport.Dispatcher = (*Dispatcher)(nil)

// Dispatcher publishes tasks to an AMQP queue.
type Dispatcher struct {
	mu         sync.RWMutex
	recovering atomic.Bool
	conn       *Connection
	ch         *amqp091.Channel
	queue      *Queue
	retryFn    func() func(context.Context) error
}

// Publish serializes a task and publishes it to the queue.
func (d *Dispatcher) Publish(ctx context.Context, task ledger.Node) error {
	body, err := json.Marshal(task)
	if err != nil {
		return err
	}

	exchange, routingKey := "", d.queue.Name
	if d.queue.ExchangeType == ExchangeTopic {
		exchange, routingKey = d.queue.Exchange, d.queue.RoutingKey
	}

	d.mu.RLock()
	err = d.ch.PublishWithContext(ctx, exchange, routingKey, false, false, amqp091.Publishing{
		ContentType: "application/json",
		Body:        body,
	})
	d.mu.RUnlock()

	if err != nil && d.recovering.CompareAndSwap(false, true) {
		go d.recover(ctx)
	}

	return err
}

// Close shuts down the channel.
func (d *Dispatcher) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.ch.Close()
}

func (d *Dispatcher) recover(ctx context.Context) {
	defer d.recovering.Store(false)

	d.mu.Lock()
	defer d.mu.Unlock()

	_ = d.ch.Close()

	retry := d.retryFn()

	for {
		if err := d.conn.reconnect(); err != nil {
			gonveyor.Logger.Error("dispatcher reconnect failed", "err", err)
			if err := retry(ctx); err != nil {
				gonveyor.Logger.Error("dispatcher gave up reconnecting", "err", err)
				return
			}
			continue
		}

		ch, err := d.conn.channel()
		if err != nil {
			gonveyor.Logger.Error("dispatcher channel open failed", "err", err)
			if err := retry(ctx); err != nil {
				gonveyor.Logger.Error("dispatcher gave up reconnecting", "err", err)
				return
			}
			continue
		}

		if err := d.declareQueue(ch); err != nil {
			_ = ch.Close()
			gonveyor.Logger.Error("dispatcher queue declare failed", "err", err)
			if err := retry(ctx); err != nil {
				gonveyor.Logger.Error("dispatcher gave up reconnecting", "err", err)
				return
			}
			continue
		}

		d.ch = ch
		gonveyor.Logger.Info("dispatcher reconnected")
		return
	}
}

func (d *Dispatcher) declareQueue(ch *amqp091.Channel) error {
	_, err := ch.QueueDeclarePassive(d.queue.Name, d.queue.Durable, d.queue.AutoDelete, false, false, nil)
	return err
}

func defaultDispatcherRetry() func() func(context.Context) error {
	return retryBackoff(RetryConfig{
		InitialDelay: time.Second,
		Factor:       2,
		MaxRetries:   5,
	})
}
