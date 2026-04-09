package keeper

import (
	"context"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/x/demurrage/types"
)

// InitGenesis initialises the demurrage module from a genesis state.
// Called once on chain start.
//
// On a fresh chain:
//   - GlobalAccumulator is set to 1.0 (identity; no demurrage has accrued yet).
//   - CurrentAnnualRate is set to DemurrageRateMin.
//   - AccountStates is empty; accounts default to refAcc = globalAcc on first touch.
//
// On an upgrade (genesis export + import):
//   - AccountStates contains the per-account lazy state from the previous chain,
//     preserving accumulated decay accurately across the upgrade.
func (k Keeper) InitGenesis(ctx sdk.Context, gs *types.GenesisState) {
	if err := k.Params.Set(ctx, gs.Params); err != nil {
		panic(err)
	}
	if err := k.State.Set(ctx, gs.State); err != nil {
		panic(err)
	}
	for _, entry := range gs.AccountStates {
		addr, err := sdk.AccAddressFromBech32(entry.Address)
		if err != nil {
			panic(err)
		}
		if err := k.AccountState.Set(ctx, addr.Bytes(), entry.DemurrageState); err != nil {
			panic(err)
		}
	}
}

// ExportGenesis returns the current module state as a GenesisState, including
// all per-account lazy-evaluation states so that accumulated decay is preserved
// across upgrades and genesis restarts.
func (k Keeper) ExportGenesis(ctx context.Context) *types.GenesisState {
	params, err := k.Params.Get(ctx)
	if err != nil {
		panic(err)
	}
	state, err := k.State.Get(ctx)
	if err != nil {
		panic(err)
	}

	var accountStates []types.AccountDemurrageStateEntry
	if err := k.AccountState.Walk(ctx, nil, func(addrBytes []byte, s types.AccountDemurrageState) (bool, error) {
		addr := sdk.AccAddress(addrBytes)
		accountStates = append(accountStates, types.AccountDemurrageStateEntry{
			Address:        addr.String(),
			DemurrageState: s,
		})
		return false, nil
	}); err != nil {
		panic(err)
	}

	return &types.GenesisState{
		Params:        params,
		State:         state,
		AccountStates: accountStates,
	}
}
