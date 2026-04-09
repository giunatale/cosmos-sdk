package types

import (
	"errors"
	"fmt"

	"cosmossdk.io/math"

	sdk "github.com/cosmos/cosmos-sdk/types"
)

// DefaultParams returns the default x/demurrage parameters.
// Rates are set to the same range as x/mint (7%–20%, 13% change) for governance familiarity.
func DefaultParams() Params {
	return Params{
		DemurrageRateMin:    math.LegacyNewDecWithPrec(7, 2),  // 7%
		DemurrageRateMax:    math.LegacyNewDecWithPrec(20, 2), // 20%
		DemurrageRateChange: math.LegacyNewDecWithPrec(13, 2), // 13% max yearly delta
		GoalBonded:          math.LegacyNewDecWithPrec(67, 2), // 67%
		EpochIdentifier:     DefaultEpochIdentifier,           // "hour"
		SinkMode:            SinkModeRedistribution,
		ExemptModuleAccounts: []string{
			"bonded_tokens_pool",
			"not_bonded_tokens_pool",
			"distribution",
			"gov",
			"community_pool",
		},
	}
}

// Validate performs basic validation on the params.
func (p Params) Validate() error {
	if err := validateDec("demurrage_rate_min", p.DemurrageRateMin, false); err != nil {
		return err
	}
	if err := validateDec("demurrage_rate_max", p.DemurrageRateMax, false); err != nil {
		return err
	}
	if err := validateDec("demurrage_rate_change", p.DemurrageRateChange, false); err != nil {
		return err
	}
	if err := validateDec("goal_bonded", p.GoalBonded, true); err != nil {
		return err
	}
	if p.DemurrageRateMin.GT(p.DemurrageRateMax) {
		return fmt.Errorf("demurrage_rate_min (%s) must be <= demurrage_rate_max (%s)",
			p.DemurrageRateMin, p.DemurrageRateMax)
	}
	if err := validateEpochIdentifier(p.EpochIdentifier); err != nil {
		return err
	}
	switch p.SinkMode {
	case SinkModeBurn, SinkModeCommunityPool, SinkModeRedistribution:
	default:
		return ErrInvalidSinkMode
	}
	for _, name := range p.ExemptModuleAccounts {
		if name == "" {
			return errors.New("exempt_module_accounts entries must not be empty strings")
		}
	}
	return nil
}

// validateEpochIdentifier ensures the epoch identifier is one of the standard
// x/epochs identifiers registered in the default genesis configuration.
// If a chain registers a custom epoch identifier it must be added here and in keys.go.
func validateEpochIdentifier(id string) error {
	switch id {
	case EpochIdentifierMinute, EpochIdentifierHour, EpochIdentifierDay, EpochIdentifierWeek:
		return nil
	case "":
		return errors.New("epoch_identifier cannot be empty")
	default:
		return fmt.Errorf("epoch_identifier %q is not a recognized x/epochs identifier; valid values: minute, hour, day, week", id)
	}
}

// validateDec validates a LegacyDec field: non-nil, non-negative, ≤ 1.
// If mustBePositive is true, zero is also rejected.
func validateDec(field string, v math.LegacyDec, mustBePositive bool) error {
	if v.IsNil() {
		return fmt.Errorf("%s cannot be nil", field)
	}
	if v.IsNegative() {
		return fmt.Errorf("%s cannot be negative: %s", field, v)
	}
	if mustBePositive && v.IsZero() {
		return fmt.Errorf("%s must be positive: %s", field, v)
	}
	if v.GT(math.LegacyOneDec()) {
		return fmt.Errorf("%s too large (max 1.0): %s", field, v)
	}
	return nil
}

// ValidateGenesis validates genesis state parameters.
func ValidateGenesis(data GenesisState) error {
	if err := data.Params.Validate(); err != nil {
		return err
	}
	if data.State.CurrentAnnualRate.IsNil() || data.State.CurrentAnnualRate.IsNegative() {
		return errors.New("genesis state current_annual_rate must be non-negative")
	}
	if data.State.GlobalAccumulator.IsNil() || data.State.GlobalAccumulator.IsNegative() {
		return errors.New("genesis state global_accumulator must be non-negative")
	}
	if data.State.GlobalAccumulator.GT(math.LegacyOneDec()) {
		return errors.New("genesis state global_accumulator must be <= 1.0")
	}
	seenAddrs := make(map[string]struct{}, len(data.AccountStates))
	for i, entry := range data.AccountStates {
		if _, err := sdk.AccAddressFromBech32(entry.Address); err != nil {
			return fmt.Errorf("account_states[%d]: invalid address %q: %w", i, entry.Address, err)
		}
		if _, seen := seenAddrs[entry.Address]; seen {
			return fmt.Errorf("account_states[%d]: duplicate address %q", i, entry.Address)
		}
		seenAddrs[entry.Address] = struct{}{}
		if entry.DemurrageState.ReferenceAccumulator.IsNil() || entry.DemurrageState.ReferenceAccumulator.IsNegative() {
			return fmt.Errorf("account_states[%d]: reference_accumulator must be non-negative", i)
		}
		if entry.DemurrageState.ReferenceAccumulator.GT(math.LegacyOneDec()) {
			return fmt.Errorf("account_states[%d]: reference_accumulator must be <= 1.0", i)
		}
		if entry.DemurrageState.ReferenceAccumulator.LT(data.State.GlobalAccumulator) {
			return fmt.Errorf("account_states[%d]: reference_accumulator (%s) < global_accumulator (%s); would produce negative levy",
				i, entry.DemurrageState.ReferenceAccumulator, data.State.GlobalAccumulator)
		}
	}
	return nil
}

// DefaultGenesisState returns the default genesis state for x/demurrage.
func DefaultGenesisState() *GenesisState {
	params := DefaultParams()
	return &GenesisState{
		Params: params,
		State: DemurrageState{
			CurrentAnnualRate: params.DemurrageRateMin,
			GlobalAccumulator: math.LegacyOneDec(), // 1.0 at genesis
		},
	}
}

// IsExemptModuleAccount returns true if the given module name is in the exempt list.
func (p Params) IsExemptModuleAccount(name string) bool {
	for _, exempt := range p.ExemptModuleAccounts {
		if exempt == name {
			return true
		}
	}
	return false
}

// IsExemptAddress returns true if the given address corresponds to a module account
// that is in the exempt list.
func (p Params) IsExemptAddress(addr sdk.AccAddress, moduleAddrFn func(string) sdk.AccAddress) bool {
	for _, name := range p.ExemptModuleAccounts {
		moduleAddr := moduleAddrFn(name)
		if moduleAddr != nil && moduleAddr.Equals(addr) {
			return true
		}
	}
	return false
}
