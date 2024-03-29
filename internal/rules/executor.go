package rules

import (
	"context"
	"encoding/json"
	"time"

	"go.uber.org/zap"

	"github.com/petuhovskiy/neon-lights/internal/app"
	"github.com/petuhovskiy/neon-lights/internal/log"
	"github.com/petuhovskiy/neon-lights/internal/rdesc"
)

type ctxkey int

const (
	ctxkeyInsidePeriodic ctxkey = iota
)

type Executor struct {
	base *app.App
}

func NewExecutor(base *app.App) *Executor {
	return &Executor{base: base}
}

func (e *Executor) ParseJSON(data json.RawMessage) (*Rule, error) {
	var desc rdesc.Rule
	err := json.Unmarshal(data, &desc)
	if err != nil {
		return nil, err
	}

	return e.CreateFromDesc(desc)
}

func (e *Executor) CreateFromDesc(desc rdesc.Rule) (*Rule, error) {
	impl, err := loadImpl(e.base, e, desc)
	if err != nil {
		return nil, err
	}

	return newRule(desc, impl)
}

func (e *Executor) Execute(ctx context.Context, r *Rule) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	var insidePeriodic bool
	if val, ok := ctx.Value(ctxkeyInsidePeriodic).(bool); ok {
		insidePeriodic = val
	}

	// can't execute nested periodic rules
	isPeriodic := r.period != nil && !insidePeriodic
	if isPeriodic {
		return e.executePeriodic(ctx, r, r.period)
	}
	return e.executeOnce(ctx, r)
}

func (e *Executor) executeOnce(ctx context.Context, r *Rule) error {
	// skip if there was a recent run
	if r.lastRun != nil && r.desc.MinInterval != nil && time.Since(*r.lastRun) < r.desc.MinInterval.Duration {
		return nil
	}

	now := time.Now()
	r.lastRun = &now

	ctx = log.Into(ctx, string(r.desc.Act))
	if r.desc.Timeout != nil {
		// we don't want to cancel context, because Execute can do background work
		ctx, _ = context.WithTimeout(ctx, r.desc.Timeout.Duration) //nolint:govet
	}
	err := r.impl.Execute(ctx)

	now = time.Now()
	r.lastRun = &now

	return err
}

func (e *Executor) executePeriodic(ctx context.Context, r *Rule, period *Period) error {
	ctx = context.WithValue(ctx, ctxkeyInsidePeriodic, true)
	ctx = log.Into(ctx, "periodic")

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		err := e.executeOnce(ctx, r)
		if err != nil {
			// TODO: add option to propagate errors
			log.Error(ctx, "rule execution failed", zap.Error(err))
		}

		period.Sleep(ctx)
	}
}
