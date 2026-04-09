package keeper

import (
	"context"
	"strconv"

	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/x/demurrage/types"
	epochstypes "github.com/cosmos/cosmos-sdk/x/epochs/types"
	stakingtypes "github.com/cosmos/cosmos-sdk/x/staking/types"
)

// Hooks is a wrapper struct that holds the Keeper and implements both
// stakingtypes.StakingHooks and epochstypes.EpochHooks.
type Hooks struct {
	k Keeper
}

var (
	_ stakingtypes.StakingHooks = Hooks{}
	_ epochstypes.EpochHooks    = Hooks{}
)

// Hooks returns the Hooks wrapper for the demurrage keeper.
func (k Keeper) Hooks() Hooks {
	return Hooks{k}
}

// ======================== EpochHooks ========================

// AfterEpochEnd is called at the end of each epoch.
// When the epoch identifier matches the module's configured epoch, the demurrage
// Rate Engine runs and the global accumulator is updated.
func (h Hooks) AfterEpochEnd(ctx context.Context, epochIdentifier string, epochNumber int64) error {
	params, err := h.k.Params.Get(ctx)
	if err != nil {
		return err
	}
	if params.EpochIdentifier != epochIdentifier {
		return nil
	}

	epochDurationSeconds := epochDurationSeconds(params.EpochIdentifier)

	// UpdateDemurrageState returns the per-epoch rate and bondingRatio so we
	// can emit the event without fetching BondedRatio a second time.
	perEpochRate, bondingRatio, err := h.k.UpdateDemurrageState(ctx, epochDurationSeconds)
	if err != nil {
		return err
	}

	state, err := h.k.State.Get(ctx)
	if err != nil {
		return err
	}

	sdkCtx := sdk.UnwrapSDKContext(ctx)
	sdkCtx.EventManager().EmitEvent(
		sdk.NewEvent(
			types.EventTypeDemurrageEpoch,
			sdk.NewAttribute(types.AttributeKeyAnnualRate, state.CurrentAnnualRate.String()),
			sdk.NewAttribute(types.AttributeKeyPerEpochRate, perEpochRate.String()),
			sdk.NewAttribute(types.AttributeKeyGlobalAccumulator, state.GlobalAccumulator.String()),
			sdk.NewAttribute(types.AttributeKeyBondingRatio, bondingRatio.String()),
			sdk.NewAttribute(types.AttributeKeyEpochNumber, strconv.FormatInt(epochNumber, 10)),
		),
	)

	return nil
}

// BeforeEpochStart is a no-op for the demurrage module.
func (h Hooks) BeforeEpochStart(_ context.Context, _ string, _ int64) error {
	return nil
}

// ======================== StakingHooks ========================
// The demurrage module intercepts all delegation lifecycle hooks that involve a
// specific delegator address. The invariant we must uphold is:
//
//	RefAcc may only advance to GlobalAcc after the accumulated levy has been collected.
//
// x/bank's DelegateCoins and UndelegateCoins bypass the send restriction entirely
// (they modify balances directly, unlike SendCoins). This means the send restriction
// alone is NOT sufficient to capture demurrage at delegation/unbonding boundaries.
//
// We hook the Before* variants (which fire before DelegateCoins deducts from the
// account) so that levy is computed on the full pre-delegation liquid balance. The
// corresponding After* hooks are a safety net: by the time they fire, MaterializeBalance
// is already a no-op because RefAcc == GlobalAcc.
//
// Lifecycle:
//   - MsgDelegate / MsgBeginRedelegate / MsgUndelegate (partial):
//     BeforeDelegationCreated / BeforeDelegationSharesModified fires → MaterializeBalance
//     → levy collected on full balance before tokens move → RefAcc = GlobalAcc.
//     AfterDelegationModified fires → MaterializeBalance → no-op (already current).
//   - MsgUndelegate (full, delegation removed):
//     BeforeDelegationSharesModified fires → MaterializeBalance → levy collected.
//     BeforeDelegationRemoved fires → MaterializeBalance → no-op.
//   - Unbonding completes (CompleteUnbonding):
//     UndelegateCoins bypasses the bank restriction.  The account's RefAcc is already
//     at GlobalAcc from the time of unbonding initiation.  The returned tokens arrive
//     and blend with the liquid balance; on the next bank operation the send restriction
//     fires and collects any levy accrued since then.  A small imprecision exists: the
//     returned tokens carry the RefAcc from unbonding initiation, not from completion,
//     so they accumulate ~ε demurrage for the unbonding period (≤ 0.6% at max rate,
//     21-day unbond). This cannot be fixed without modifying x/bank's DelegateCoins
//     path, which is out of scope.

func (h Hooks) AfterValidatorCreated(_ context.Context, _ sdk.ValAddress) error { return nil }
func (h Hooks) BeforeValidatorModified(_ context.Context, _ sdk.ValAddress) error { return nil }
func (h Hooks) AfterValidatorRemoved(_ context.Context, _ sdk.ConsAddress, _ sdk.ValAddress) error {
	return nil
}
func (h Hooks) AfterValidatorBonded(_ context.Context, _ sdk.ConsAddress, _ sdk.ValAddress) error {
	return nil
}
func (h Hooks) AfterValidatorBeginUnbonding(_ context.Context, _ sdk.ConsAddress, _ sdk.ValAddress) error {
	return nil
}
func (h Hooks) BeforeValidatorSlashed(_ context.Context, _ sdk.ValAddress, _ math.LegacyDec) error {
	return nil
}

// BeforeDelegationCreated materializes the delegator's balance before a new delegation
// record is created and DelegateCoins deducts from the account. This ensures levy is
// computed on the full pre-delegation liquid balance, not the reduced post-delegation one.
func (h Hooks) BeforeDelegationCreated(ctx context.Context, delAddr sdk.AccAddress, _ sdk.ValAddress) error {
	return h.k.MaterializeBalance(ctx, delAddr)
}

// BeforeDelegationSharesModified materializes the delegator's balance before any
// modification (add-to-existing delegation, redelegate, partial/full unbond). Firing
// before the operation ensures levy is charged on the full pre-modification balance.
// This is the primary defence against a re-delegation RefAcc-reset loophole: any
// attempt to fast-forward RefAcc is preceded by levy collection, making it economically
// neutral.
func (h Hooks) BeforeDelegationSharesModified(ctx context.Context, delAddr sdk.AccAddress, _ sdk.ValAddress) error {
	return h.k.MaterializeBalance(ctx, delAddr)
}

// BeforeDelegationRemoved materializes the delegator's balance before a full unbond
// removes the delegation record. Redundant with BeforeDelegationSharesModified (which
// fires first for full unbonds), but included as a belt-and-suspenders safeguard.
func (h Hooks) BeforeDelegationRemoved(ctx context.Context, delAddr sdk.AccAddress, _ sdk.ValAddress) error {
	return h.k.MaterializeBalance(ctx, delAddr)
}

// AfterDelegationModified is called after a delegation is created or modified. By the
// time this fires, one of the Before* hooks has already collected the levy and advanced
// RefAcc to GlobalAcc, so this is a no-op in normal operation. It is kept as a
// belt-and-suspenders safeguard for any execution path that skips the Before* hooks.
func (h Hooks) AfterDelegationModified(ctx context.Context, delAddr sdk.AccAddress, _ sdk.ValAddress) error {
	return h.k.MaterializeBalance(ctx, delAddr)
}

// AfterUnbondingInitiated is a no-op for the demurrage module.
func (h Hooks) AfterUnbondingInitiated(_ context.Context, _ uint64) error {
	return nil
}
