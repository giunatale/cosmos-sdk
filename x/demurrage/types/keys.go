package types

import "cosmossdk.io/collections"

const (
	// ModuleName is the module name constant used in many places.
	ModuleName = "demurrage"

	// StoreKey is the string store representation.
	StoreKey = ModuleName

	// RouterKey is the message route for demurrage.
	RouterKey = ModuleName

	// DefaultEpochIdentifier is the x/epochs epoch this module listens to by default.
	DefaultEpochIdentifier = "hour"

	// Valid epoch identifiers matching the default x/epochs genesis configuration.
	EpochIdentifierMinute = "minute"
	EpochIdentifierHour   = "hour"
	EpochIdentifierDay    = "day"
	EpochIdentifierWeek   = "week"

	// SinkModeBurn burns levied tokens, shrinking total supply.
	SinkModeBurn = "burn"

	// SinkModeCommunityPool sends levied tokens to the community pool.
	SinkModeCommunityPool = "community_pool"

	// SinkModeRedistribution deposits levied tokens into the fee pool for staker distribution.
	SinkModeRedistribution = "redistribution"
)

// Collections key prefixes.
var (
	ParamsKey        = collections.NewPrefix(0)
	DemurrageStateKey = collections.NewPrefix(1)
	AccountStateKey  = collections.NewPrefix(2)
)
