package keeper

import (
	"context"

	sdk "github.com/cosmos/cosmos-sdk/types"
	authtypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	"github.com/cosmos/cosmos-sdk/x/demurrage/types"
)

// routeToSink takes coins that have already been sent to the demurrage module account
// and routes them according to the configured SinkMode parameter.
func (k Keeper) routeToSink(ctx context.Context, coins sdk.Coins) error {
	params, err := k.Params.Get(ctx)
	if err != nil {
		return err
	}

	switch params.SinkMode {
	case types.SinkModeBurn:
		return k.sinkBurn(ctx, coins)

	case types.SinkModeCommunityPool:
		return k.sinkCommunityPool(ctx, coins)

	case types.SinkModeRedistribution:
		return k.sinkRedistribution(ctx, coins)

	default:
		return types.ErrInvalidSinkMode
	}
}

// sinkBurn permanently destroys the coins, shrinking total supply.
func (k Keeper) sinkBurn(ctx context.Context, coins sdk.Coins) error {
	return k.bankKeeper.BurnCoins(ctx, types.ModuleName, coins)
}

// sinkCommunityPool sends the coins to the community pool via x/distribution.
// Supply remains constant; funds are redirected to ecosystem development.
func (k Keeper) sinkCommunityPool(ctx context.Context, coins sdk.Coins) error {
	moduleAddr := k.accountKeeper.GetModuleAddress(types.ModuleName)
	return k.distributionKeeper.FundCommunityPool(ctx, coins, moduleAddr)
}

// sinkRedistribution deposits the coins into the distribution fee pool, exactly as
// if they were transaction fees. The existing x/distribution allocation logic
// (community tax, proposer reward, staker reward) applies without modification.
// Supply remains constant; this is closest to the current inflation model in net effect.
func (k Keeper) sinkRedistribution(ctx context.Context, coins sdk.Coins) error {
	// Move coins from the demurrage module account to the fee collector (feeCollectorName).
	// x/distribution picks them up via its BeginBlocker, just like regular fees.
	return k.bankKeeper.SendCoinsFromModuleToModule(
		ctx,
		types.ModuleName,
		authtypes.FeeCollectorName,
		coins,
	)
}
