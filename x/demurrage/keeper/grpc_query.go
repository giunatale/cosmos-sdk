package keeper

import (
	"context"

	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/cosmos-sdk/x/demurrage/types"
)

type queryServer struct {
	Keeper
}

// NewQueryServerImpl returns an implementation of the demurrage QueryServer interface.
func NewQueryServerImpl(keeper Keeper) types.QueryServer {
	return &queryServer{Keeper: keeper}
}

var _ types.QueryServer = queryServer{}

// Params returns the current demurrage module parameters.
func (qs queryServer) Params(ctx context.Context, _ *types.QueryParamsRequest) (*types.QueryParamsResponse, error) {
	params, err := qs.Keeper.Params.Get(ctx)
	if err != nil {
		return nil, err
	}
	return &types.QueryParamsResponse{Params: params}, nil
}

// DemurrageState returns the current module-level dynamic state.
func (qs queryServer) DemurrageState(ctx context.Context, _ *types.QueryDemurrageStateRequest) (*types.QueryDemurrageStateResponse, error) {
	state, err := qs.Keeper.State.Get(ctx)
	if err != nil {
		return nil, err
	}
	return &types.QueryDemurrageStateResponse{State: state}, nil
}

// EffectiveBalance returns the post-demurrage (projected) balance for an account.
// This is a read-only projection; it does NOT modify chain state.
func (qs queryServer) EffectiveBalance(ctx context.Context, req *types.QueryEffectiveBalanceRequest) (*types.QueryEffectiveBalanceResponse, error) {
	addr, err := sdk.AccAddressFromBech32(req.Address)
	if err != nil {
		return nil, err
	}

	effective, err := qs.Keeper.EffectiveBalance(ctx, addr, req.Denom)
	if err != nil {
		return nil, err
	}

	return &types.QueryEffectiveBalanceResponse{EffectiveBalance: effective}, nil
}

