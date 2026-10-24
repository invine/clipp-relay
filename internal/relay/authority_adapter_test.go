package relay

import "context"

// These controlled protocol adapters have no database work. Budget and racing
// authority fixtures implement their own protection; production uses auth.Server.

func (a *countingAuthority) WithServingUnit(ctx context.Context, work func(context.Context)) bool {
	work(ctx)
	return ctx.Err() == nil
}

func (a *countingAuthority) WithAccountGuards(ctx context.Context, _ []string, work func(context.Context)) bool {
	work(ctx)
	return ctx.Err() == nil
}

func (a discoveryErrorAuthority) WithServingUnit(ctx context.Context, work func(context.Context)) bool {
	work(ctx)
	return ctx.Err() == nil
}

func (a discoveryErrorAuthority) WithAccountGuards(ctx context.Context, _ []string, work func(context.Context)) bool {
	work(ctx)
	return ctx.Err() == nil
}

func (a expiringAuthority) WithServingUnit(ctx context.Context, work func(context.Context)) bool {
	work(ctx)
	return ctx.Err() == nil
}

func (a expiringAuthority) WithAccountGuards(ctx context.Context, _ []string, work func(context.Context)) bool {
	work(ctx)
	return ctx.Err() == nil
}

func (a failingAuthority) WithServingUnit(ctx context.Context, work func(context.Context)) bool {
	work(ctx)
	return ctx.Err() == nil
}

func (a failingAuthority) WithAccountGuards(ctx context.Context, _ []string, work func(context.Context)) bool {
	work(ctx)
	return ctx.Err() == nil
}

func (a lifecycleAuthority) WithServingUnit(ctx context.Context, work func(context.Context)) bool {
	work(ctx)
	return ctx.Err() == nil
}

func (a lifecycleAuthority) WithAccountGuards(ctx context.Context, _ []string, work func(context.Context)) bool {
	work(ctx)
	return ctx.Err() == nil
}

func (a *observedAuthority) WithServingUnit(ctx context.Context, work func(context.Context)) bool {
	work(ctx)
	return ctx.Err() == nil
}

func (a *observedAuthority) WithAccountGuards(ctx context.Context, _ []string, work func(context.Context)) bool {
	work(ctx)
	return ctx.Err() == nil
}

func (a *policyAuthority) WithServingUnit(ctx context.Context, work func(context.Context)) bool {
	work(ctx)
	return ctx.Err() == nil
}

func (a sameAccountAuthority) WithServingUnit(ctx context.Context, work func(context.Context)) bool {
	work(ctx)
	return ctx.Err() == nil
}

func (a sameAccountAuthority) WithAccountGuards(ctx context.Context, _ []string, work func(context.Context)) bool {
	work(ctx)
	return ctx.Err() == nil
}

func (a shortRVAuthority) WithServingUnit(ctx context.Context, work func(context.Context)) bool {
	work(ctx)
	return ctx.Err() == nil
}

func (a shortRVAuthority) WithAccountGuards(ctx context.Context, _ []string, work func(context.Context)) bool {
	work(ctx)
	return ctx.Err() == nil
}

func (a transportLimitAuthority) WithServingUnit(ctx context.Context, work func(context.Context)) bool {
	work(ctx)
	return ctx.Err() == nil
}

func (a transportLimitAuthority) WithAccountGuards(ctx context.Context, _ []string, work func(context.Context)) bool {
	work(ctx)
	return ctx.Err() == nil
}

func (a twoAuthority) WithServingUnit(ctx context.Context, work func(context.Context)) bool {
	work(ctx)
	return ctx.Err() == nil
}

func (a twoAuthority) WithAccountGuards(ctx context.Context, _ []string, work func(context.Context)) bool {
	work(ctx)
	return ctx.Err() == nil
}

func (a wireAuthority) WithServingUnit(ctx context.Context, work func(context.Context)) bool {
	work(ctx)
	return ctx.Err() == nil
}

func (a wireAuthority) WithAccountGuards(ctx context.Context, _ []string, work func(context.Context)) bool {
	work(ctx)
	return ctx.Err() == nil
}

func (a zeroCapAuthority) WithServingUnit(ctx context.Context, work func(context.Context)) bool {
	work(ctx)
	return ctx.Err() == nil
}

func (a zeroCapAuthority) WithAccountGuards(ctx context.Context, _ []string, work func(context.Context)) bool {
	work(ctx)
	return ctx.Err() == nil
}
