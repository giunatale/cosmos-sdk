package types

import errorsmod "cosmossdk.io/errors"

var (
	ErrInvalidSinkMode = errorsmod.Register(ModuleName, 2, "invalid sink mode: must be 'burn', 'community_pool', or 'redistribution'")
	ErrInvalidParams   = errorsmod.Register(ModuleName, 3, "invalid demurrage parameters")
)
