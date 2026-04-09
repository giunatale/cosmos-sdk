package keeper

import "github.com/cosmos/cosmos-sdk/x/demurrage/types"

// epochDurationSeconds returns the duration in seconds for a validated epoch identifier.
// The identifier is guaranteed to be one of the four recognised values because params
// validation rejects anything else, so no keeper lookup is needed.
func epochDurationSeconds(identifier string) int64 {
	switch identifier {
	case types.EpochIdentifierMinute:
		return 60
	case types.EpochIdentifierDay:
		return 60 * 60 * 24
	case types.EpochIdentifierWeek:
		return 60 * 60 * 24 * 7
	default: // EpochIdentifierHour and any unexpected value
		return 60 * 60
	}
}
