# x/demurrage

## 1. Overview

The `x/demurrage` module implements a **demurrage-based staking incentive system** intended as a drop-in replacement for the `x/mint` inflationary model.

Rather than minting new tokens to reward stakers and dilute non-stakers, demurrage **contracts liquid non-bonded balances** over time while leaving bonded (staked) balances untouched. The economic incentive is identical — rational actors are pushed toward a target bonding ratio — but the mechanism operates through supply contraction rather than expansion.

---

## 2. Economic Model

### 2.1 Equivalence with Inflation

The standard inflationary model (x/mint) distributes stakers an annual reward `r` by minting new tokens and distributing them to the bonded pool. If total supply is `S`, bonded fraction is `b`, and the reward rate is `r`:

- Bonded balance at time `t+1`: `b·S·(1 + r/(b·S))` ← receives fresh minted tokens
- Non-bonded balance at time `t+1`: `(1-b)·S` ← unchanged in absolute terms

In **relative** purchasing power terms, the non-bonded holder's share falls from `(1-b)` to roughly `(1-b)/(1 + r_inflation)`.

Under the demurrage model, no new tokens are minted. Instead, non-bonded balances decay at rate `d`:

- Bonded balance at time `t+1`: `b·S` (unchanged — exempt)
- Non-bonded balance at time `t+1`: `(1-b)·S·(1 - d)`

For the game-theoretic incentive to be identical, both models must produce the same **relative** transfer from non-stakers to stakers. Setting the two ratios equal:

```
1/(1 + r_inflation) ≡ (1 - d)
  ⟹  d ≡ r_inflation / (1 + r_inflation)
```

For the range of rates used in practice (7%–20%), this correction is small: at 13%, `d ≈ 11.5%` vs `r = 13%`. The rate bounds in parameters are chosen with this equivalence in mind.

The key insight: **both models produce the same relative wealth transfer** from non-stakers to stakers. They differ only in the direction of expression — "stakers gain tokens" vs "non-stakers lose tokens" — but the incentive gradient is identical.

### 2.2 Supply Trajectory

Under each sink mode the trajectory of total supply `S` differs:

| Sink Mode | Supply at time t |
|---|---|
| `burn` | `S(t) = S(0) · GlobalAccumulator(t)` — monotonically shrinking |
| `community_pool` | `S(t) = S(0)` — constant; redistribution to public goods |
| `redistribution` | `S(t) = S(0)` — constant; levied tokens returned to stakers via fee distribution |

Under `burn`, total supply follows the global accumulator. After `n` epochs at per-epoch rate `ε`:

```
GlobalAccumulator(n) = (1 - ε)^n
```

At a sustained 10% annual rate with hourly epochs (`ε ≈ 0.0000120`), after 10 years `GlobalAccumulator ≈ 0.9^10 ≈ 0.349`. After 100 years, `≈ 0.9^100 ≈ 2.66 × 10⁻⁵`.

This does **not** mean holders lose 99.999% of their wealth — an account that stakes during this period keeps its balance intact. Demurrage only accumulates during periods of liquid, non-bonded holding.

### 2.3 Tax Character

A significant economic distinction from inflation: under the inflationary model, stakers receive new tokens — a taxable receipt event in many jurisdictions. Under demurrage, stakers receive nothing; non-stakers silently lose value. This difference has several implications:

- **Staker taxation**: Inflation model: stakers receive tokens → taxable income event. Demurrage model: stakers receive no new tokens → no direct taxable event (tax professionals should be consulted on specific jurisdictions).
- **Non-staker taxation**: Demurrage is a balance reduction; whether this constitutes a deductible loss is jurisdiction-specific.
- **Redistribution mode**: Levied tokens flow to the fee collector and appear as validator/delegator rewards, which may be taxable — functionally similar to the inflationary model's tax character despite the different mechanism.

---

## 3. Core Algorithm

### 3.1 Global Accumulator Pattern (Lazy Evaluation)

Iterating over every account each epoch does not scale on a chain with millions of accounts. Instead, the module uses a **global demurrage accumulator** pattern, directly analogous to the reward-per-token pattern used in `x/distribution`:

```
GlobalAccumulator ∈ (0, 1]   // starts at 1.0 at genesis
```

Each epoch, after computing the per-epoch levy rate `ε`:

```
GlobalAccumulator *= (1 - ε)
```

Each account stores a **reference accumulator** — the value of `GlobalAccumulator` at the last time the account's balance was "touched" (sent, received, delegated, or explicitly queried):

```protobuf
message AccountDemurrageState {
  string reference_accumulator = 1;  // GlobalAccumulator at last materialization
  int64  reference_block       = 2;  // block height at last materialization (informational)
}
```

The effective (post-demurrage) balance is computed without any state write:

```
EffectiveBalance = locked + spendable × (GlobalAccumulator / ReferenceAccumulator)
```

Because `GlobalAccumulator ≤ ReferenceAccumulator` always (the global only ever decreases, the ref was set when it was higher), the ratio is in `(0, 1]`. The levy is:

```
levy = spendable - spendable × (GlobalAcc / RefAcc)
     = spendable × (1 - GlobalAcc/RefAcc)
     = spendable × (RefAcc - GlobalAcc) / RefAcc
```

**Materialization** collects this levy, routes it to the configured sink, then advances `ReferenceAccumulator = GlobalAccumulator`. This is **O(1) per account touch** — no iteration required.

Accounts that have never been touched default to `ReferenceAccumulator = GlobalAccumulator` at first access, so no retroactive demurrage is charged for balances that predate the module.

### 3.2 Rate Engine

The rate engine is the direct analogue of `x/mint`'s `NextInflationRate` control loop, but **inverted**: when bonding is below target the demurrage rate rises (non-stakers are taxed more aggressively), and when bonding exceeds target the rate falls (pressure is relaxed).

The rate adjusts proportionally to the distance from the target bonding ratio, scaled by the fraction of a year elapsed since the last update:

```
Δt = epochDurationSeconds / secondsPerYear   // fraction of year elapsed

if BondingRatio < GoalBonded:
    rateChange = (1 - BondingRatio/GoalBonded) × DemurrageRateChange × Δt
    NewRate    = min(CurrentRate + rateChange, DemurrageRateMax)

else if BondingRatio > GoalBonded:
    rateChange = (BondingRatio/GoalBonded - 1) × DemurrageRateChange × Δt
    NewRate    = max(CurrentRate - rateChange, DemurrageRateMin)

else:
    NewRate = CurrentRate   // at equilibrium — no change
```

At equilibrium (`BondingRatio == GoalBonded`), the rate is stable and the system does not oscillate.

### 3.3 Per-Epoch Rate Computation

The per-epoch levy factor uses the **compound formula** so that applying it across all epochs in a year yields exactly the annual rate:

```
PerEpochRate = 1 - (1 - AnnualRate)^(1/EpochsPerYear)
```

This is computed via a 4-term Taylor series for `ln(1-r)` and a 3-term Taylor series for `exp(x)`. The approximation has less than 0.01% relative error for all expected parameter combinations (`r ≤ 20%`, epochs hourly to weekly).

**Why not the linear approximation?** The `x/mint` module uses the linear approximation `r/n` because it runs every block and the per-block rate is tiny (`r/5256000` at the standard 6s block time), making the linear error negligible. This module operates at much coarser granularity — the epoch could be daily or weekly. The linear approximation introduces ~10% relative error at daily epochs with a 20% annual rate. The compound formula avoids this at trivial computational cost.

### 3.4 Vesting Account Handling

Demurrage is only applied to the **spendable (vested)** portion of an account. The locked (unvested) portion is exempt because:

1. The holder cannot choose to stake unvested tokens
2. Taxing tokens the holder cannot move would be punitive without recourse
3. It is consistent with the economic argument — demurrage incentivizes *choosing* to stake; there is no choice for locked tokens

Implementation: `MaterializeBalance` reads `SpendableCoins` from `x/bank` and applies the levy only to the spendable fraction:

```
locked            = StoredBalance - SpendableCoins
effectiveSpendable = spendable × (GlobalAcc / RefAcc)
levy              = spendable - effectiveSpendable

EffectiveBalance  = locked + effectiveSpendable
```

The locked portion is always returned at full face value.

---

## 4. Parameters

All parameters are governance-controlled via `MsgUpdateParams`.

| Parameter | Type | Default | Description |
| --- | --- | --- | --- |
| `demurrage_rate_min` | Dec | `0.07` (7%) | Floor of the annualised demurrage rate |
| `demurrage_rate_max` | Dec | `0.20` (20%) | Ceiling of the annualised demurrage rate |
| `demurrage_rate_change` | Dec | `0.13` (13%) | Maximum yearly change in the rate |
| `goal_bonded` | Dec | `0.67` (67%) | Target bonding ratio |
| `epoch_identifier` | string | `"hour"` | x/epochs identifier (`minute`, `hour`, `day`, `week`) |
| `sink_mode` | SinkMode | `redistribution` | Destination for levied tokens |
| `exempt_module_accounts` | []string | see below | Module accounts excluded from demurrage |

The parameter range mirrors `x/mint` defaults so that governance proposals, documentation, and mental models carry over with minimal retraining.

**Default exempt module accounts:**

- `bonded_tokens_pool` — the bonded staking pool; staked tokens must never be taxed
- `not_bonded_tokens_pool` — the unbonding pool; tokens already in the unbonding queue are exempt
- `distribution` — the distribution module's fee pool; internal accounting
- `gov` — the governance deposit account; deposits should not be eroded while in escrow
- `community_pool` — the community pool; public-goods funds should not self-tax

IBC escrow accounts are **not** in the exempt list. See §8 for the full rationale and the exchange-rate escrow mechanism that maintains economic soundness without exemption.

Validation rejects any `epoch_identifier` not in the recognized set (`minute`, `hour`, `day`, `week`).

---

## 5. State

### 5.1 Module-Level State

```protobuf
message DemurrageState {
  string current_annual_rate = 1;  // current annualised rate (sdk.Dec)
  string global_accumulator  = 2;  // running product of (1 - per_epoch_rate), starts at 1.0
}
```

`GlobalAccumulator` is monotonically non-increasing. It is never reset; it accumulates since genesis, shrinking asymptotically toward zero over very long time horizons.

### 5.2 Per-Account State

```protobuf
message AccountDemurrageState {
  string reference_accumulator = 1;  // GlobalAccumulator at last materialization
  int64  reference_block       = 2;  // block height at last materialization (informational)
}
```

Keyed by raw address bytes in the module's KV store. Entries are **garbage-collected** when a materialization finds a zero balance — the account re-defaults to the current `GlobalAccumulator` on next touch, which is semantically equivalent to storing `RefAcc = GlobalAcc`.

### 5.3 Safety: Self-Heal on Invalid State

`GlobalAccumulator` should only ever decrease. If a state migration bug artificially raises it above a stored `ReferenceAccumulator` (producing `GlobalAcc > RefAcc`), the ratio `GlobalAcc / RefAcc > 1` would produce a negative levy.

Both `EffectiveBalance` and `MaterializeBalance` detect this condition:

- `EffectiveBalance`: returns the raw balance unchanged (read-only, no state write)
- `MaterializeBalance`: resets `RefAcc = GlobalAcc` with zero levy and logs a warning

This self-heal is a defensive safeguard; it should never trigger during normal operation. Invariant monitoring should alert on the warning log.

---

## 6. Sink Modes

| Mode | Mechanism | Effect on Total Supply |
| --- | --- | --- |
| `burn` | `x/bank.BurnCoins` — tokens permanently destroyed | Supply shrinks monotonically |
| `community_pool` | Tokens sent to `x/distribution` community pool | Supply constant; accrues to public goods |
| `redistribution` | Tokens deposited into the fee collector (`x/auth.FeeCollectorName`) — distributed to stakers/proposers by `x/distribution` exactly like tx fees | Supply constant; economically equivalent to inflation |

`redistribution` is the default and represents the smallest behavioral change from `x/mint`. Governance can migrate to `community_pool` or `burn` via parameter update.

**Economic note on redistribution**: In redistribution mode, the total token supply is constant. Non-stakers lose `levy` tokens per epoch; these same tokens are received by stakers as fee-distribution rewards. The net effect is indistinguishable in aggregate from the inflation model — it is purely a supply accounting difference.

---

## 7. Module Integration

### 7.1 Epoch Hook — AfterEpochEnd

When `AfterEpochEnd` fires for the configured `EpochIdentifier`, the following sequence runs atomically:

1. Fetch current `BondedRatio` from `x/staking`
2. Compute new annual rate via Rate Engine (§3.2)
3. Derive per-epoch rate from new annual rate and epoch duration (§3.3)
4. Update `GlobalAccumulator *= (1 - PerEpochRate)`
5. Persist updated `DemurrageState`
6. Emit `EventDemurrageEpoch`

No other periodic work runs. The module has **no BeginBlock or EndBlock logic**; all periodic computation is driven by epoch hooks.

The epoch duration is derived deterministically from the validated `EpochIdentifier` string (`minute = 60s`, `hour = 3600s`, `day = 86400s`, `week = 604800s`). No runtime lookup into `x/epochs` is required — the mapping is fixed by param validation.

### 7.2 Staking Hooks

The demurrage module hooks every delegation lifecycle event that involves a specific delegator address. The core invariant is:

> `RefAcc` may only advance to `GlobalAcc` after the accumulated levy has been collected.

**Why the send restriction alone is insufficient:** `x/bank`'s `DelegateCoins` and `UndelegateCoins` modify balances directly without invoking the send restriction. If demurrage relied solely on the send restriction, a user could call `MsgBeginRedelegate` (which fires staking hooks but triggers no bank send restriction on the delegator's liquid balance) and silently advance `RefAcc` to `GlobalAcc`, erasing any pending levy. All delegation hooks therefore call `MaterializeBalance` — levy is collected first, and RefAcc advances only as a result of materialization.

**Why `Before*` hooks:** they fire before `DelegateCoins` moves tokens, ensuring levy is computed on the full pre-delegation liquid balance. The corresponding `After*` hook is then a no-op since `RefAcc == GlobalAcc` already.

| Hook | When it fires | Action |
| --- | --- | --- |
| `BeforeDelegationCreated` | Before a new delegation record is created | `MaterializeBalance` |
| `BeforeDelegationSharesModified` | Before adding to an existing delegation, redelegating, or any unbond | `MaterializeBalance` |
| `BeforeDelegationRemoved` | Before a full unbond removes the delegation record | `MaterializeBalance` (belt-and-suspenders) |
| `AfterDelegationModified` | After a delegation is created or modified | `MaterializeBalance` (no-op if Before* already fired) |

**Full lifecycle:**

1. **Delegation** — `BeforeDelegationCreated` fires → `MaterializeBalance` → levy collected, `RefAcc = GlobalAcc`. Then `DelegateCoins` moves tokens to `bonded_tokens_pool` (exempt).
2. **Re-delegation** — `BeforeDelegationSharesModified` fires → `MaterializeBalance` → levy collected on the full current liquid balance before the operation. `RefAcc` can only advance after paying pending demurrage — the loophole is closed.
3. **While bonded** — tokens reside in `bonded_tokens_pool` (exempt) → zero demurrage.
4. **Unbonding initiated** — `BeforeDelegationSharesModified` fires → `MaterializeBalance` → levy collected. Tokens move to `not_bonded_tokens_pool` (exempt) → zero demurrage during the unbonding period (typically 21 days).
5. **Unbonding completes** — `x/staking` calls `UndelegateCoinsFromModuleToAccount`. This bypasses the send restriction (same as `DelegateCoins`). The returned tokens blend into the account's liquid balance with `RefAcc` at `GlobalAcc` from step 4. On the next bank operation the send restriction fires and collects any levy since then.

**Residual imprecision:** because `UndelegateCoins` bypasses the send restriction, returned tokens carry `RefAcc` from unbonding initiation, not completion — they accrue approximately `ε × unbonding_days` demurrage during the unbonding period. At maximum parameters (20% annual, 21-day unbond) this is at most `≈ 1.3%`. Fixing this would require modifying `x/bank`'s delegation path, which is out of scope.

### 7.3 Bank Send Restriction

The module registers a `SendRestrictionFn` with `x/bank` during app wiring (`SetSendRestriction`). This function is called before every coin transfer and materializes both participants:

```
sendRestrictionFn(ctx, fromAddr, toAddr, amt):
    MaterializeBalance(ctx, fromAddr)  // collect levy before the send deducts from sender
    MaterializeBalance(ctx, toAddr)    // sync receiver's RefAcc before new coins arrive
    return toAddr, nil
```

**Why materialize both?**

- **Sender**: the levy must be deducted before the transfer amount is checked against the sender's balance. Materializing first ensures the sender's actual available balance is used.
- **Receiver**: if the receiver has a stale `RefAcc` and new coins arrive, those coins would be merged into the balance and their per-token age would be incorrectly backdated to when the receiver's last touch occurred. Materializing the receiver first resets their `RefAcc` to the current global, then the coins arrive on a clean slate.

The demurrage module account itself is always exempt to prevent re-entrancy: when levied coins are moved from user accounts into the module account and then to the sink, no demurrage is re-applied.

---

## 8. IBC — Exchange-Rate Escrow Model

> **This section describes the architectural design for IBC demurrage soundness. The IBC middleware is not yet implemented. The design here is the intended target.**

### 8.1 The Problem: Why Exempting IBC Escrow Breaks the System

In the inflationary model, IBC'd tokens don't earn staking rewards. The holder of `ibc/ATONE` vouchers on a remote chain forfeits the reward stream — they pay an opportunity cost equal to the inflation rate. The game-theoretic equilibrium is preserved: holding `ibc/ATONE` instead of bonded ATONE on the Hub costs them `r` per year in missed rewards.

If the IBC escrow account were exempt from demurrage, the parallel would shatter:

- Non-stakers on the Hub: balance decays at rate `d`
- Non-stakers who IBC'd out: balance in escrow unchanged (exempt); their `ibc/ATONE` is redeemable 1:1 forever

This transforms IBC into a **costless demurrage shelter**. Any rational actor not wanting to stake would simply IBC-transfer their tokens out, wait indefinitely, and return them without ever having been taxed. The game-theoretic foundation of the system collapses — the incentive to stake evaporates for anyone with IBC access.

**No exemption is correct.** Demurrage accumulates on the escrow account exactly as it does on any other liquid holding. The economic question becomes: how does the redemption of `ibc/ATONE` vouchers work when the underlying escrow has been reduced by demurrage?

### 8.2 The Solution: Exchange-Rate Escrow

The escrow account is a normal account subject to demurrage. Over time the escrowed token balance shrinks via the global accumulator. The Hub tracks the **total outstanding vouchers** issued to counterparty chains, and derives an **exchange rate** as the ratio of current escrowed value to outstanding vouchers:

```
ExchangeRate = CurrentEscrowBalance / TotalVouchersOutstanding
```

At genesis `ExchangeRate = 1.0`. As demurrage accumulates on the escrow, `CurrentEscrowBalance` shrinks while `TotalVouchersOutstanding` remains fixed (outstanding vouchers on counterparty chains are not reduced by Hub-side demurrage). The exchange rate falls below 1.0.

When a holder of `ibc/ATONE` returns tokens to the Hub, they do not receive face value. They receive:

```
Redeemable = FaceValue × ExchangeRate
```

This is the precise economic equivalent of "you held non-bonded ATONE on the Hub for this period, so you owe the demurrage that would have accrued." The user does not receive fewer vouchers on the remote chain — they hold exactly what was issued. But when redeeming, the purchasing power of each voucher has depreciated, exactly mirroring the depreciation that would have occurred had the tokens never left the Hub.

### 8.3 Global IBC Demurrage State

The Hub maintains:

```protobuf
message GlobalIBCDemurrageState {
  // Total face-value of vouchers outstanding across all IBC channels for the bond denom.
  // Increases on outbound transfers, decreases on successful inbound returns and timeouts.
  string total_vouchers_outstanding = 1;

  // Cached exchange rate: current_escrow_balance / total_vouchers_outstanding.
  // Updated each epoch in AfterEpochEnd. Used for queries and inbound packet processing.
  string exchange_rate = 2;
}
```

### 8.4 IBC Middleware Lifecycle

The demurrage module wraps the standard ICS-20 transfer application as an IBC middleware.

#### Outbound: Hub → Remote (ICS-20 MsgTransfer)

```
OnSendPacket(packet):
    if denom == StakingDenom:
        MaterializeBalance(ctx, escrowAccount)    // snapshot current effective escrow
        TotalVouchersOutstanding += packet.Amount  // record issued voucher face value
    pass to ICS-20
```

#### Inbound: Remote → Hub (return transfer)

```
OnRecvPacket(packet):
    if denom == ibc/<StakingDenom>:
        MaterializeBalance(ctx, escrowAccount)    // apply any pending demurrage to escrow
        currentEscrow = GetBalance(escrowAccount, StakingDenom)
        exchangeRate  = currentEscrow / TotalVouchersOutstanding
        redeemable    = packet.Amount × exchangeRate     // haircut applied here
        override packet.Amount = redeemable              // ICS-20 will unescrow this amount
        TotalVouchersOutstanding -= packet.Amount        // remove face value from outstanding
    pass to ICS-20 with modified amount
```

The account receiving on the Hub gets `redeemable` native tokens (less than face value), and `redeemable` tokens are moved out of escrow to them. The remainder stays in escrow (representing demurrage collected on behalf of future redeemers / the system).

#### Timeout or failed acknowledgement

```
OnTimeoutPacket / OnAcknowledgementPacket(failure):
    TotalVouchersOutstanding -= packet.Amount  // vouchers never reached counterparty; cancel
    // Escrow is unchanged; the tokens were never actually transferred
    pass to ICS-20
```

### 8.5 Exchange Rate Update

The exchange rate in `GlobalIBCDemurrageState` is refreshed once per epoch in `AfterEpochEnd` after the global accumulator is updated:

```
totalEscrow = ibcTransferKeeper.GetTotalEscrowForDenom(ctx, StakingDenom)
if TotalVouchersOutstanding > 0:
    ExchangeRate = totalEscrow / TotalVouchersOutstanding
else:
    ExchangeRate = 1.0
```

The escrow balance already reflects demurrage because the global accumulator update just decayed it. No per-packet epoch updates are needed; the rate is always fresh within one epoch.

### 8.6 Design Properties

- **Hub-side only**: counterparty chains require no changes; they issue and accept vouchers at face value as normal. The haircut is applied exclusively on Hub-side redemption.
- **ICS-20 compatible**: the middleware transparently wraps the standard transfer application; from the counterparty's perspective, nothing changes.
- **Multi-hop safe**: consider `ATONE → ChainA → ChainB`. The escrow is on the Hub (for the Hub→ChainA leg). Demurrage accumulates there. When ChainB returns tokens to ChainA, and ChainA returns to the Hub, the exchange rate at the Hub escrow correctly reflects total accrued demurrage over the entire holding period regardless of the routing. Intermediate chains are unaffected.
- **Queryable exchange rate**: wallets, DEXes, relayers, and bridges can read the current exchange rate and display accurate redemption values in real time.
- **ICA compatibility**: Interchain Account-controlled accounts holding liquid tokens on the Hub are subject to demurrage normally through the bank send restriction; no special ICA handling is needed.
- **Escrow asymptote**: demurrage via multiplicative decay is asymptotic — the escrow balance never reaches zero. After 100 years at 10% annual demurrage, `ExchangeRate ≈ 0.9^100 ≈ 2.66 × 10⁻⁵`. The escrow is nearly empty but non-zero. In practice, dust-threshold GC would sweep residual state long before this point.

---

## 9. Queries

| RPC | REST | Description |
| --- | --- | --- |
| `Params` | `GET /cosmos/demurrage/v1/params` | Current governance parameters |
| `DemurrageState` | `GET /cosmos/demurrage/v1/state` | Current annual rate and global accumulator |
| `EffectiveBalance` | `GET /cosmos/demurrage/v1/effective_balance/{address}/{denom}` | Post-demurrage balance for an account (read-only, no state write) |

`EffectiveBalance` applies demurrage only to the bond denom. For any other denom, the raw stored balance is returned unchanged — demurrage is bond-denom-only by design.

The per-epoch rate is derivable off-chain from `DemurrageState.CurrentAnnualRate` and `Params.EpochIdentifier`, so no dedicated `AnnualRate` or `PerEpochRate` endpoint is provided.

---

## 10. Events

### EventDemurrageEpoch

Emitted once per epoch when the global accumulator is updated.

| Attribute | Value |
|---|---|
| `annual_rate` | New annual demurrage rate after rate engine update |
| `per_epoch_rate` | Per-epoch levy factor applied this epoch |
| `global_accumulator` | Updated global accumulator value |
| `bonding_ratio` | Current bonded/total ratio used for rate computation |
| `epoch_number` | Epoch sequence number from x/epochs |

### EventDemurrageApplied

Emitted each time an account's balance is materialized (levy collected).

| Attribute | Value |
|---|---|
| `account` | Address of the account |
| `amount_levied` | Amount of bond denom levied |
| `new_balance` | Post-levy effective spendable balance |

---

## 11. Genesis

### InitGenesis

On fresh chain start: `GlobalAccumulator = 1.0`, `CurrentAnnualRate = DemurrageRateMin`, `AccountStates` is empty.

On upgrade from chain export: all `AccountStates` are restored, preserving the lazy-evaluation state and preventing a "forgiveness" event where all accounts would default to the current (already-decayed) `GlobalAccumulator`.

### ExportGenesis

Exports all per-account states by walking the `AccountState` collections map, ensuring accurate demurrage continuity across upgrades and genesis restarts.

### Genesis Validation

`ValidateGenesis` enforces:

- All `Params` fields satisfy `Validate()`
- `CurrentAnnualRate` is non-negative
- `GlobalAccumulator ∈ [0, 1]`
- Each `AccountStates` entry has a valid bech32 address, no duplicates, `ReferenceAccumulator ∈ [GlobalAccumulator, 1.0]`

The last bound (`RefAcc ≥ GlobalAcc`) prevents importing state that would immediately trigger the self-heal path on every account touch after genesis.

---

## 12. Edge Cases and Known Limitations

### 12.1 Dust Account Accumulation

Demurrage is multiplicative and asymptotic — balances approach zero but never reach zero. Without intervention, accounts with very small spendable balances would accumulate in state indefinitely, each with a sub-unit balance that can neither be transferred nor staked.

**Implemented fix:** During `MaterializeBalance`, after computing `effectiveSpendable = floor(spendable × GlobalAcc/RefAcc)`, if `effectiveSpendable == 0` (the spendable portion has decayed below one base unit), the entire spendable balance is levied rather than leaving it. This is the precise threshold: a zero-truncated effective balance is uncollectable by any normal operation. The sweep is immediate and requires no additional parameter.

When the entire balance (spendable + locked) is levied, the per-account state entry is garbage-collected. `EffectiveBalance` already returns `locked` when `effectiveSpendable` truncates to zero, so query behaviour is consistent.

### 12.2 CosmWasm Contract Accounts

CosmWasm contract accounts are not module accounts and are not in the exempt list. They accumulate demurrage on their liquid bond-denom holdings exactly like regular accounts. Contract developers holding bond-denom balances should be aware that their contract's balance will erode over time if tokens are not moved or staked.

If a contract is meant to hold bond-denom tokens for extended periods (e.g., a vesting contract, a DAO treasury), it should be added to the `exempt_module_accounts` list via governance, or designed to stake its holdings.

### 12.3 Validator Commission

Validator commission is distributed as rewards via `x/distribution`, which is exempt from demurrage. Commission flows from the distribution module to the validator operator address when the operator explicitly withdraws (`MsgWithdrawValidatorCommission`). This withdrawal is a bank transfer and triggers the send restriction, materializing the operator's account. No special handling is required — the commission is correctly taxed from the moment it lands in the operator's liquid balance.

### 12.4 Missing Invariant Registration

The module does not register invariants via `module.HasInvariants`. The following invariants should be registered:

- `GlobalAccumulator ∈ (0, 1]` and non-increasing across blocks
- No stored `AccountDemurrageState` has `ReferenceAccumulator < GlobalAccumulator`
- The total of all per-account levies emitted in a materialization epoch equals the decrease in total spendable supply (in `burn` mode)

### 12.5 Migration from x/mint

When upgrading a live chain:

1. Set `x/mint` `InflationMin = InflationMax = 0` (stop minting)
2. Initialize `x/demurrage` with `GlobalAccumulator = 1.0`, `CurrentAnnualRate = DemurrageRateMin`
3. All existing accounts default `ReferenceAccumulator = 1.0` on first touch — no retroactive demurrage for pre-migration balances

A gradual transition is also possible: run both modules with `x/mint` inflation decreasing over N governance cycles while `x/demurrage` rate increases from 0, maintaining economic equivalence throughout the transition.

---

## 13. Implementation Notes

### Epoch Duration Without x/epochs Dependency

The module does not call into `x/epochs` at runtime for duration lookups. Since `EpochIdentifier` is validated at param-set time to be one of `{minute, hour, day, week}`, the duration in seconds is derived directly from the identifier string (`epochDurationSeconds` in `keeper/epoch_util.go`). This avoids adding `EpochsKeeper` as a keeper dependency for a lookup of a fixed constant.

### EpochsPerYear Calculation

Uses 365 days/year (not 365.25). The 0.07% difference is negligible compared to rate-adjustment granularity and is consistent with how `x/epochs` schedules wall-clock epochs.

### Bond Denom Guard

`EffectiveBalance` checks the denom against the bond denom before performing any demurrage computation. For non-bond-denom queries, the raw stored balance is returned immediately. This is important for correctness: demurrage is defined only for the bonding token.

The exempt check runs before the bond denom lookup because it requires only an in-memory params read and a module address comparison, while `BondDenom` involves a staking keeper call.
