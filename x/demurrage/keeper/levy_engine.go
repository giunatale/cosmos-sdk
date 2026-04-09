package keeper

import (
	"context"
	"errors"

	"cosmossdk.io/collections"
	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/x/demurrage/types"
)

// GetAccountState returns the AccountDemurrageState for an address.
// If no state exists yet (e.g. the account predates the module), a default state
// with ReferenceAccumulator = current GlobalAccumulator is returned so that no
// retroactive demurrage is charged for balances that existed before the module.
func (k Keeper) GetAccountState(ctx context.Context, addr sdk.AccAddress) (types.AccountDemurrageState, error) {
	state, err := k.AccountState.Get(ctx, addr.Bytes())
	if err == nil {
		return state, nil
	}
	if !errors.Is(err, collections.ErrNotFound) {
		return types.AccountDemurrageState{}, err
	}
	// Not found — first touch. Default to the current global accumulator so
	// no retroactive demurrage is charged for balances that predate the module.
	globalAcc, err := k.getGlobalAccumulator(ctx)
	if err != nil {
		return types.AccountDemurrageState{}, err
	}
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	return types.AccountDemurrageState{
		ReferenceAccumulator: globalAcc,
		ReferenceBlock:       sdkCtx.BlockHeight(),
	}, nil
}

// SetAccountState persists the AccountDemurrageState for an address.
func (k Keeper) SetAccountState(ctx context.Context, addr sdk.AccAddress, state types.AccountDemurrageState) error {
	return k.AccountState.Set(ctx, addr.Bytes(), state)
}

// IsExempt returns true if the address is the demurrage module account itself
// (prevents re-entrancy) or is a module account listed in the exempt params.
func (k Keeper) IsExempt(ctx context.Context, addr sdk.AccAddress) bool {
	params, err := k.Params.Get(ctx)
	if err != nil {
		return false
	}
	moduleAddr := k.accountKeeper.GetModuleAddress(types.ModuleName)
	if moduleAddr != nil && moduleAddr.Equals(addr) {
		return true
	}
	return params.IsExemptAddress(addr, k.accountKeeper.GetModuleAddress)
}

// EffectiveBalance computes the post-demurrage balance for an account without
// modifying state. Used for queries and balance checks.
//
// Demurrage is only applied to the bond denom. For any other denom, the raw
// stored balance is returned unchanged.
//
//	EffectiveBalance = locked + spendable * (GlobalAccumulator / ReferenceAccumulator)
func (k Keeper) EffectiveBalance(ctx context.Context, addr sdk.AccAddress, denom string) (math.Int, error) {
	// Exempt check is free (no I/O beyond params); do it before the BondDenom RPC.
	if k.IsExempt(ctx, addr) {
		bal := k.bankKeeper.GetBalance(ctx, addr, denom)
		return bal.Amount, nil
	}

	// Demurrage only applies to the bond denom; return raw balance for everything else.
	bondDenom, err := k.stakingKeeper.BondDenom(ctx)
	if err != nil {
		return math.Int{}, err
	}
	if denom != bondDenom {
		bal := k.bankKeeper.GetBalance(ctx, addr, denom)
		return bal.Amount, nil
	}

	accountState, err := k.GetAccountState(ctx, addr)
	if err != nil {
		return math.Int{}, err
	}

	globalAcc, err := k.getGlobalAccumulator(ctx)
	if err != nil {
		return math.Int{}, err
	}

	// Safety: globalAcc monotonically decreases; refAcc is always ≥ globalAcc in
	// normal operation (refAcc was set when global was higher).  The impossible case
	// is globalAcc > refAcc (e.g. after a migration bug that artificially raises the
	// global accumulator), which would give ratio > 1 and thus negative levy.  In
	// that case self-heal by returning the raw balance and resetting refAcc.
	if globalAcc.GT(accountState.ReferenceAccumulator) {
		k.Logger(ctx).Warn("demurrage: global accumulator exceeds reference; self-healing",
			"address", addr.String(),
			"ref_acc", accountState.ReferenceAccumulator.String(),
			"global_acc", globalAcc.String(),
		)
		stored := k.bankKeeper.GetBalance(ctx, addr, denom)
		return stored.Amount, nil
	}

	stored := k.bankKeeper.GetBalance(ctx, addr, denom)
	if stored.IsZero() {
		return math.ZeroInt(), nil
	}

	// Use spendable coins to respect locked vesting amounts:
	// demurrage is only applied to the vested (transferable) portion.
	spendable := k.bankKeeper.SpendableCoins(ctx, addr).AmountOf(denom)
	locked := stored.Amount.Sub(spendable)

	if spendable.IsZero() {
		// All tokens are locked (vesting) — nothing to levy.
		return stored.Amount, nil
	}

	// EffectiveSpendable = spendable * (globalAcc / refAcc)
	ratio := globalAcc.Quo(accountState.ReferenceAccumulator)
	effectiveSpendable := math.LegacyNewDecFromInt(spendable).Mul(ratio).TruncateInt()

	return locked.Add(effectiveSpendable), nil
}

// MaterializeBalance applies accumulated demurrage to an account and routes the
// levy to the configured sink. This is an O(1) operation per account touch.
//
// Dust sweep: if the spendable portion decays below one base unit (effectiveSpendable
// truncates to zero), the entire spendable balance is levied. This prevents sub-unit
// residuals from accumulating in state indefinitely without ever being collectible.
//
// The function is a no-op if:
//   - The account is on the exempt list.
//   - The computed levy is zero (no balance or no accumulator change since last touch).
func (k Keeper) MaterializeBalance(ctx context.Context, addr sdk.AccAddress) error {
	if k.IsExempt(ctx, addr) {
		return nil
	}

	accountState, err := k.GetAccountState(ctx, addr)
	if err != nil {
		return err
	}

	globalAcc, err := k.getGlobalAccumulator(ctx)
	if err != nil {
		return err
	}

	// If the reference accumulator is already current, nothing to do.
	if accountState.ReferenceAccumulator.Equal(globalAcc) {
		return nil
	}

	// Safety: globalAcc can only DECREASE (each epoch multiplies by (1-rate) < 1).
	// So refAcc (set when global was higher) is always ≥ globalAcc in normal
	// operation.  The impossible case is globalAcc > refAcc — this arises only
	// after a state migration bug that artificially raises the global accumulator,
	// which would produce a ratio > 1 and thus negative levy.  Self-heal by
	// resetting refAcc to the current global (no levy for this period).
	if globalAcc.GT(accountState.ReferenceAccumulator) {
		k.Logger(ctx).Warn("demurrage: global accumulator exceeds reference; self-healing",
			"address", addr.String(),
			"ref_acc", accountState.ReferenceAccumulator.String(),
			"global_acc", globalAcc.String(),
		)
		sdkCtx := sdk.UnwrapSDKContext(ctx)
		accountState.ReferenceAccumulator = globalAcc
		accountState.ReferenceBlock = sdkCtx.BlockHeight()
		return k.SetAccountState(ctx, addr, accountState)
	}

	bondDenom, err := k.stakingKeeper.BondDenom(ctx)
	if err != nil {
		return err
	}

	stored := k.bankKeeper.GetBalance(ctx, addr, bondDenom)
	if stored.IsZero() {
		// No balance to levy — delete the account state entirely (GC).
		// The account will re-default to the current global accumulator on next touch,
		// which is equivalent to the state we would otherwise persist here.
		return k.AccountState.Remove(ctx, addr.Bytes())
	}

	// Apply demurrage only to spendable (non-locked) tokens.
	spendable := k.bankKeeper.SpendableCoins(ctx, addr).AmountOf(bondDenom)
	if spendable.IsZero() {
		// All tokens locked (vesting); advance the reference accumulator only.
		sdkCtx := sdk.UnwrapSDKContext(ctx)
		accountState.ReferenceAccumulator = globalAcc
		accountState.ReferenceBlock = sdkCtx.BlockHeight()
		return k.SetAccountState(ctx, addr, accountState)
	}

	ratio := globalAcc.Quo(accountState.ReferenceAccumulator)
	effectiveSpendable := math.LegacyNewDecFromInt(spendable).Mul(ratio).TruncateInt()
	levy := spendable.Sub(effectiveSpendable)

	// Dust sweep: if the spendable portion has decayed below one base unit, levy
	// the entire spendable balance.  A sub-unit effectiveSpendable can never be
	// transferred or staked, so keeping it in state serves no purpose.
	if effectiveSpendable.IsZero() {
		levy = spendable
	}

	if !levy.IsPositive() {
		// No levy this epoch (rounding or already current).
		sdkCtx := sdk.UnwrapSDKContext(ctx)
		accountState.ReferenceAccumulator = globalAcc
		accountState.ReferenceBlock = sdkCtx.BlockHeight()
		return k.SetAccountState(ctx, addr, accountState)
	}

	// Collect the levy from the account into the demurrage module account.
	levyCoins := sdk.NewCoins(sdk.NewCoin(bondDenom, levy))
	if err := k.bankKeeper.SendCoinsFromAccountToModule(ctx, addr, types.ModuleName, levyCoins); err != nil {
		return err
	}

	// Route the levy to the configured sink.
	if err := k.routeToSink(ctx, levyCoins); err != nil {
		return err
	}

	// Emit event for wallets and block explorers.
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	sdkCtx.EventManager().EmitEvent(
		sdk.NewEvent(
			types.EventTypeDemurrageApplied,
			sdk.NewAttribute(types.AttributeKeyAccount, addr.String()),
			sdk.NewAttribute(types.AttributeKeyAmountLevied, levy.String()),
			sdk.NewAttribute(types.AttributeKeyNewBalance, stored.Amount.Sub(levy).String()),
		),
	)

	// Update per-account state, or GC if the account is now empty.
	// The locked portion is whatever was not spendable.
	locked := stored.Amount.Sub(spendable)
	if locked.IsZero() {
		// No remaining balance; remove the state entry so this account is not
		// tracked until new tokens arrive.
		return k.AccountState.Remove(ctx, addr.Bytes())
	}
	accountState.ReferenceAccumulator = globalAcc
	accountState.ReferenceBlock = sdkCtx.BlockHeight()
	return k.SetAccountState(ctx, addr, accountState)
}

// getGlobalAccumulator is a convenience helper to fetch the global accumulator from state.
func (k Keeper) getGlobalAccumulator(ctx context.Context) (math.LegacyDec, error) {
	state, err := k.State.Get(ctx)
	if err != nil {
		return math.LegacyDec{}, err
	}
	return state.GlobalAccumulator, nil
}
