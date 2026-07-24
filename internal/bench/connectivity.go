package bench

import (
	"context"

	"dnsbench/internal/model"
)

type connWatch struct {
	engine  *Engine
	strikes int
}

func (w *connWatch) observe(ctx context.Context, total int, allTimedOut bool, active []model.Server) error {
	if total == 0 || !allTimedOut {
		w.strikes = 0
		return nil
	}
	w.strikes++
	if w.strikes < 2 {
		return nil
	}
	w.strikes = 0
	return w.recover(ctx, active)
}

func (w *connWatch) recover(ctx context.Context, active []model.Server) error {
	e := w.engine
	e.emit(ctx, model.Event{
		Type: model.EvConnLost,
		Msg:  "connectivity lost: two consecutive rounds where every query timed out",
	})
	e.gate.pause()
	if len(active) == 0 || len(e.cfg.CachedDomains) == 0 {
		e.emit(ctx, model.Event{Type: model.EvWarn, Msg: "connectivity watch cannot probe: no cached domains are configured"})
		e.gate.unpause()
		return ctx.Err()
	}
	target := active[0]
	name := e.cfg.CachedDomains[0]
	for {
		if !sleepCtx(ctx, e.canaryInterval) {
			return ctx.Err()
		}
		res := e.singleQuery(ctx, target, name)
		if res.ValidFor(model.CatCached) {
			e.emit(ctx, model.Event{Type: model.EvConnRestored, Msg: "connectivity restored"})
			e.gate.unpause()
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
	}
}
