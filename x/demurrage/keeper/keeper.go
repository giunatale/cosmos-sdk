package keeper

import (
	"context"
	"fmt"

	"cosmossdk.io/collections"
	storetypes "cosmossdk.io/core/store"
	"cosmossdk.io/log"

	"github.com/cosmos/cosmos-sdk/codec"
	sdk "github.com/cosmos/cosmos-sdk/types"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
	"github.com/cosmos/cosmos-sdk/x/demurrage/types"
)

// Keeper of the x/demurrage store.
type Keeper struct {
	cdc          codec.BinaryCodec
	storeService storetypes.KVStoreService
	authority    string

	accountKeeper      types.AccountKeeper
	bankKeeper         types.BankKeeper
	stakingKeeper      types.StakingKeeper
	distributionKeeper types.DistributionKeeper

	Schema collections.Schema

	// Params stores the governance-controlled module parameters.
	Params collections.Item[types.Params]

	// State stores the module-level dynamic state: current annual rate + global accumulator.
	State collections.Item[types.DemurrageState]

	// AccountState stores the per-account lazy-evaluation state, keyed by raw address bytes.
	AccountState collections.Map[[]byte, types.AccountDemurrageState]
}

// NewKeeper creates a new demurrage Keeper instance.
func NewKeeper(
	cdc codec.BinaryCodec,
	storeService storetypes.KVStoreService,
	ak types.AccountKeeper,
	bk types.BankKeeper,
	sk types.StakingKeeper,
	dk types.DistributionKeeper,
	authority string,
) Keeper {
	// ensure the demurrage module account exists
	if addr := ak.GetModuleAddress(types.ModuleName); addr == nil {
		panic(fmt.Sprintf("the x/%s module account has not been set", types.ModuleName))
	}

	sb := collections.NewSchemaBuilder(storeService)

	k := Keeper{
		cdc:                cdc,
		storeService:       storeService,
		authority:          authority,
		accountKeeper:      ak,
		bankKeeper:         bk,
		stakingKeeper:      sk,
		distributionKeeper: dk,
		Params: collections.NewItem(
			sb,
			types.ParamsKey,
			"params",
			codec.CollValue[types.Params](cdc),
		),
		State: collections.NewItem(
			sb,
			types.DemurrageStateKey,
			"demurrage_state",
			codec.CollValue[types.DemurrageState](cdc),
		),
		AccountState: collections.NewMap(
			sb,
			types.AccountStateKey,
			"account_demurrage_state",
			collections.BytesKey,
			codec.CollValue[types.AccountDemurrageState](cdc),
		),
	}

	schema, err := sb.Build()
	if err != nil {
		panic(err)
	}
	k.Schema = schema

	return k
}

// GetAuthority returns the x/demurrage module's authority address string.
func (k Keeper) GetAuthority() string { return k.authority }

// Logger returns a module-specific logger.
func (k Keeper) Logger(ctx context.Context) log.Logger {
	sdkCtx := sdk.UnwrapSDKContext(ctx)
	return sdkCtx.Logger().With("module", "x/"+types.ModuleName)
}

// SetSendRestriction registers the demurrage MaterializeBalance hook with the bank module.
// This must be called during app wiring (after NewKeeper).
func (k Keeper) SetSendRestriction() {
	k.bankKeeper.AppendSendRestriction(banktypes.SendRestrictionFn(k.sendRestrictionFn))
}

// sendRestrictionFn is the bank.SendRestrictionFn implementation that materialises
// both sender and receiver balances before a transfer executes.
func (k Keeper) sendRestrictionFn(ctx context.Context, fromAddr, toAddr sdk.AccAddress, amt sdk.Coins) (sdk.AccAddress, error) {
	// Materialise the sender so the levy is deducted before the transfer checks.
	if err := k.MaterializeBalance(ctx, fromAddr); err != nil {
		return toAddr, err
	}
	// Materialise the receiver so its reference accumulator is current before
	// new coins arrive (prevents a stale reference from being used on the merged balance).
	if err := k.MaterializeBalance(ctx, toAddr); err != nil {
		return toAddr, err
	}
	return toAddr, nil
}

