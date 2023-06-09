package rules

import (
	"context"
	"encoding/json"
	"reflect"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/petuhovskiy/neon-lights/internal/app"
	"github.com/petuhovskiy/neon-lights/internal/conf"
	"github.com/petuhovskiy/neon-lights/internal/log"
	"github.com/petuhovskiy/neon-lights/internal/models"
	"github.com/petuhovskiy/neon-lights/internal/rdesc"
	"github.com/petuhovskiy/neon-lights/internal/repos"
)

const DGRUpdateInterval = time.Second * 5

type DoGlobalRules struct {
	config         *conf.App
	executor       *Executor
	globalRuleRepo *repos.GlobalRuleRepo
	updateInterval time.Duration

	mu            sync.Mutex
	lastUpdate    time.Time
	dbRules       []models.GlobalRule
	loadedRules   []*Rule
	globalContext context.Context
	cancelContext func()
}

type DoGlobalRulesArgs struct {
	UpdateInterval *rdesc.Duration
}

func NewDoGlobalRules(a *app.App, executor *Executor, j json.RawMessage) (*DoGlobalRules, error) {
	var args DoGlobalRulesArgs
	err := json.Unmarshal(j, &args)
	if err != nil {
		return nil, err
	}

	updateInterval := DGRUpdateInterval
	if args.UpdateInterval != nil {
		updateInterval = args.UpdateInterval.Duration
	}

	return &DoGlobalRules{
		config:         a.Config,
		executor:       executor,
		globalRuleRepo: a.Repo.GlobalRule,
		updateInterval: updateInterval,
	}, nil
}

func compareRules(a []models.GlobalRule, b []models.GlobalRule) bool {
	if len(a) != len(b) {
		return false
	}

	return reflect.DeepEqual(a, b)
}

func (r *DoGlobalRules) fetchRules(ctx context.Context) ([]*Rule, context.Context, error) {
	ctx = log.Into(ctx, "fetchRules")

	r.mu.Lock()
	defer r.mu.Unlock()

	if time.Since(r.lastUpdate) < r.updateInterval {
		return r.loadedRules, r.globalContext, nil
	}

	dbRules, err := r.globalRuleRepo.AllEnabled()
	if err != nil {
		return nil, nil, err
	}
	ts := time.Now()

	if compareRules(r.dbRules, dbRules) {
		return r.loadedRules, r.globalContext, nil
	}

	log.Info(ctx, "global rules updated, loading", zap.Int("count", len(dbRules)))
	var loadedRules []*Rule
	for _, dbRule := range dbRules {
		log.Info(ctx, "loading rule", zap.Any("desc", dbRule.Desc))
		rule, err := r.executor.ParseJSON(dbRule.Desc)
		if err != nil {
			log.Error(ctx, "failed to load rule", zap.Error(err))
			return nil, nil, err
		}

		loadedRules = append(loadedRules, rule)
	}

	if r.cancelContext != nil {
		r.cancelContext()
	}

	r.dbRules = dbRules
	r.loadedRules = loadedRules
	r.lastUpdate = ts
	r.globalContext, r.cancelContext = context.WithCancel(ctx)
	return loadedRules, r.globalContext, nil
}

func (r *DoGlobalRules) Execute(fctx context.Context) error {
	rules, ctx, err := r.fetchRules(fctx)
	if err != nil {
		return err
	}

	// TODO: merge contexts, now only global is used

	for _, rule := range rules {
		err := r.executor.Execute(ctx, rule)
		if err != nil {
			log.Error(ctx, "failed to execute rule", zap.Error(err))
		}
	}
	log.Info(ctx, "executed global rules", zap.Int("count", len(rules)))
	return nil
}
