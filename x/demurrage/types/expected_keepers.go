package types

import (
	"context"

	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"
	banktypes "github.com/cosmos/cosmos-sdk/x/bank/types"
)

// AccountKeeper defines the account contract required by the demurrage module.
type AccountKeeper interface {
	GetModuleAddress(name string) sdk.AccAddress
}

// BankKeeper defines the bank contract required by the demurrage module.
type BankKeeper interface {
	// SpendableCoins returns the spendable (non-locked) balance for an account.
	// This correctly excludes locked vesting amounts, so demurrage is only applied
	// to the vested (free) portion of vesting accounts.
	SpendableCoins(ctx context.Context, addr sdk.AccAddress) sdk.Coins

	// GetBalance returns the raw stored balance (pre-demurrage).
	GetBalance(ctx context.Context, addr sdk.AccAddress, denom string) sdk.Coin

	// SendCoinsFromAccountToModule moves coins from a user account to a module account.
	// Used to collect the demurrage levy from each account.
	SendCoinsFromAccountToModule(ctx context.Context, senderAddr sdk.AccAddress, recipientModule string, amt sdk.Coins) error

	// SendCoinsFromModuleToModule moves coins between module accounts (e.g. demurrage → distribution).
	SendCoinsFromModuleToModule(ctx context.Context, senderModule, recipientModule string, amt sdk.Coins) error

	// BurnCoins destroys coins from a module account (used when SinkMode = "burn").
	BurnCoins(ctx context.Context, moduleName string, amt sdk.Coins) error

	// AppendSendRestriction registers a send restriction function that is called
	// before every coin transfer.  The demurrage module uses this to materialise
	// balances on the fly when an account is touched.
	AppendSendRestriction(restriction banktypes.SendRestrictionFn)
}

// StakingKeeper defines the staking contract required by the demurrage module.
type StakingKeeper interface {
	// BondedRatio returns the fraction of total supply that is currently bonded.
	BondedRatio(ctx context.Context) (math.LegacyDec, error)

	// BondDenom returns the staking bond denomination (e.g. "uatone").
	BondDenom(ctx context.Context) (string, error)
}

// DistributionKeeper defines the distribution contract required by the demurrage module.
// Only the community-pool sink mode requires the distribution keeper; the redistribution
// sink routes directly to the fee collector via BankKeeper.SendCoinsFromModuleToModule.
type DistributionKeeper interface {
	// FundCommunityPool sends coins to the community pool (SinkMode = "community_pool").
	FundCommunityPool(ctx context.Context, amount sdk.Coins, sender sdk.AccAddress) error
}

