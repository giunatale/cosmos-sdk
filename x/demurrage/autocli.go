package demurrage

import (
	autocliv1 "cosmossdk.io/api/cosmos/autocli/v1"

	demurragev1 "github.com/cosmos/cosmos-sdk/x/demurrage/types"
)

// AutoCLIOptions implements the autocli.HasAutoCLIConfig interface.
func (am AppModule) AutoCLIOptions() *autocliv1.ModuleOptions {
	return &autocliv1.ModuleOptions{
		Query: &autocliv1.ServiceCommandDescriptor{
			Service: demurragev1.Query_serviceDesc.ServiceName,
			RpcCommandOptions: []*autocliv1.RpcCommandOptions{
				{
					RpcMethod: "Params",
					Use:       "params",
					Short:     "Query the current demurrage module parameters",
				},
				{
					RpcMethod: "DemurrageState",
					Use:       "state",
					Short:     "Query the current demurrage state (annual rate, global accumulator)",
				},
				{
					RpcMethod: "EffectiveBalance",
					Use:       "effective-balance [address] [denom]",
					Short:     "Query the post-demurrage (effective) balance for an account",
					PositionalArgs: []*autocliv1.PositionalArgDescriptor{
						{ProtoField: "address"},
						{ProtoField: "denom"},
					},
				},
},
		},
		Tx: &autocliv1.ServiceCommandDescriptor{
			Service: demurragev1.Msg_serviceDesc.ServiceName,
			RpcCommandOptions: []*autocliv1.RpcCommandOptions{
				{
					RpcMethod: "UpdateParams",
					Skip:      true, // governance-gated; submit via gov proposal
				},
			},
		},
	}
}
