// Copyright 2020 Coinbase, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package results

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math/big"
	"os"
	"strconv"

	"github.com/coinbase/rosetta-cli/configuration"

	"github.com/coinbase/rosetta-sdk-go/asserter"
	"github.com/coinbase/rosetta-sdk-go/fetcher"
	"github.com/coinbase/rosetta-sdk-go/storage"
	"github.com/coinbase/rosetta-sdk-go/syncer"
	"github.com/coinbase/rosetta-sdk-go/types"
	"github.com/coinbase/rosetta-sdk-go/utils"
	"github.com/fatih/color"
	"github.com/olekukonko/tablewriter"
)

// EndCondition contains the type of
// end condition and any detail associated
// with the stop.
type EndCondition struct {
	Type   configuration.CheckDataEndCondition `json:"type"`
	Detail string                              `json:"detail"`
}

// CheckDataResults contains any error that occurred
// on a check:data run, the outcome of certain tests,
// and a collection of interesting stats.
type CheckDataResults struct {
	Error        string          `json:"error"`
	EndCondition *EndCondition   `json:"end_condition"`
	Tests        *CheckDataTests `json:"tests"`
	Stats        *CheckDataStats `json:"stats"`
}

// Print logs CheckDataResults to the console.
func (c *CheckDataResults) Print() {
	if len(c.Error) > 0 {
		fmt.Printf("\n")
		color.Red("Error: %s", c.Error)
	}

	if c.EndCondition != nil {
		fmt.Printf("\n")
		color.Green("Success: %s [%s]", c.EndCondition.Type, c.EndCondition.Detail)
	}

	fmt.Printf("\n")
	if c.Tests != nil {
		c.Tests.Print()
		fmt.Printf("\n")
	}
	if c.Stats != nil {
		c.Stats.Print()
		fmt.Printf("\n")
	}
}

// Output writes *CheckDataResults to the provided
// path.
func (c *CheckDataResults) Output(path string) {
	if len(path) > 0 {
		writeErr := utils.SerializeAndWrite(path, c)
		if writeErr != nil {
			log.Printf("%s: unable to save results\n", writeErr.Error())
		}
	}
}

// CheckDataStats contains interesting stats that
// are counted while running the check:data.
type CheckDataStats struct {
	Blocks                  int64   `json:"blocks"`
	Orphans                 int64   `json:"orphans"`
	Transactions            int64   `json:"transactions"`
	Operations              int64   `json:"operations"`
	ActiveReconciliations   int64   `json:"active_reconciliations"`
	InactiveReconciliations int64   `json:"inactive_reconciliations"`
	ReconciliationCoverage  float64 `json:"reconciliation_coverage"`
}

// Print logs CheckDataStats to the console.
func (c *CheckDataStats) Print() {
	table := tablewriter.NewWriter(os.Stdout)
	table.SetRowLine(true)
	table.SetRowSeparator("-")
	table.SetHeader([]string{"check:data Stats", "Description", "Value"})
	table.Append([]string{"Blocks", "# of blocks synced", strconv.FormatInt(c.Blocks, 10)})
	table.Append([]string{"Orphans", "# of blocks orphaned", strconv.FormatInt(c.Orphans, 10)})
	table.Append(
		[]string{
			"Transactions",
			"# of transaction processed",
			strconv.FormatInt(c.Transactions, 10),
		},
	)
	table.Append(
		[]string{"Operations", "# of operations processed", strconv.FormatInt(c.Operations, 10)},
	)
	table.Append(
		[]string{
			"Active Reconciliations",
			"# of reconciliations performed after seeing an account in a block",
			strconv.FormatInt(c.ActiveReconciliations, 10),
		},
	)
	table.Append(
		[]string{
			"Inactive Reconciliations",
			"# of reconciliation performed on randomly selected accounts",
			strconv.FormatInt(c.InactiveReconciliations, 10),
		},
	)
	table.Append(
		[]string{
			"Reconciliation Coverage",
			"% of accounts that have been reconciled",
			fmt.Sprintf("%f%%", c.ReconciliationCoverage*utils.OneHundred),
		},
	)

	table.Render()
}

// ComputeCheckDataStats returns a populated CheckDataStats.
func ComputeCheckDataStats(
	ctx context.Context,
	counters *storage.CounterStorage,
	balances *storage.BalanceStorage,
) *CheckDataStats {
	if counters == nil {
		return nil
	}

	blocks, err := counters.Get(ctx, storage.BlockCounter)
	if err != nil {
		log.Printf("%s: cannot get block counter", err.Error())
		return nil
	}

	orphans, err := counters.Get(ctx, storage.OrphanCounter)
	if err != nil {
		log.Printf("%s: cannot get orphan counter", err.Error())
		return nil
	}

	txs, err := counters.Get(ctx, storage.TransactionCounter)
	if err != nil {
		log.Printf("%s: cannot get transaction counter", err.Error())
		return nil
	}

	ops, err := counters.Get(ctx, storage.OperationCounter)
	if err != nil {
		log.Printf("%s: cannot get operations counter", err.Error())
		return nil
	}

	activeReconciliations, err := counters.Get(ctx, storage.ActiveReconciliationCounter)
	if err != nil {
		log.Printf("%s: cannot get active reconciliations counter", err.Error())
		return nil
	}

	inactiveReconciliations, err := counters.Get(ctx, storage.InactiveReconciliationCounter)
	if err != nil {
		log.Printf("%s: cannot get inactive reconciliations counter", err.Error())
		return nil
	}

	stats := &CheckDataStats{
		Blocks:                  blocks.Int64(),
		Orphans:                 orphans.Int64(),
		Transactions:            txs.Int64(),
		Operations:              ops.Int64(),
		ActiveReconciliations:   activeReconciliations.Int64(),
		InactiveReconciliations: inactiveReconciliations.Int64(),
	}

	if balances != nil {
		coverage, err := balances.ReconciliationCoverage(ctx, 0)
		if err != nil {
			log.Printf("%s: cannot get reconcile coverage", err.Error())
			return nil
		}

		stats.ReconciliationCoverage = coverage
	}

	return stats
}

// CheckDataProgress contains information
// about check:data's syncing progress.
type CheckDataProgress struct {
	Blocks        int64   `json:"blocks"`
	Tip           int64   `json:"tip"`
	Completed     float64 `json:"completed"`
	Rate          float64 `json:"rate"`
	TimeRemaining string  `json:"time_remaining"`
}

// ComputeCheckDataProgress returns
// a populated *CheckDataProgress.
func ComputeCheckDataProgress(
	ctx context.Context,
	fetcher *fetcher.Fetcher,
	network *types.NetworkIdentifier,
	counters *storage.CounterStorage,
) *CheckDataProgress {
	networkStatus, fetchErr := fetcher.NetworkStatusRetry(ctx, network, nil)
	if fetchErr != nil {
		fmt.Printf("%s: cannot get network status", fetchErr.Err.Error())
		return nil
	}
	tipIndex := networkStatus.CurrentBlockIdentifier.Index

	blocks, err := counters.Get(ctx, storage.BlockCounter)
	if err != nil {
		fmt.Printf("%s: cannot get block counter", err.Error())
		return nil
	}

	if blocks.Sign() == 0 { // wait for at least 1 block to be processed
		return nil
	}

	orphans, err := counters.Get(ctx, storage.OrphanCounter)
	if err != nil {
		fmt.Printf("%s: cannot get orphan counter", err.Error())
		return nil
	}

	adjustedBlocks := blocks.Int64() - orphans.Int64()
	if tipIndex-adjustedBlocks <= 0 { // return if no blocks to sync
		return nil
	}

	elapsedTime, err := counters.Get(ctx, TimeElapsedCounter)
	if err != nil {
		fmt.Printf("%s: cannot get elapsed time", err.Error())
		return nil
	}

	if elapsedTime.Sign() == 0 { // wait for at least some elapsed time
		return nil
	}

	blocksPerSecond := new(big.Float).Quo(new(big.Float).SetInt64(adjustedBlocks), new(big.Float).SetInt(elapsedTime))
	blocksPerSecondFloat, _ := blocksPerSecond.Float64()
	blocksSynced := new(big.Float).Quo(new(big.Float).SetInt64(adjustedBlocks), new(big.Float).SetInt64(tipIndex))
	blocksSyncedFloat, _ := blocksSynced.Float64()

	return &CheckDataProgress{
		Blocks:        adjustedBlocks,
		Tip:           tipIndex,
		Completed:     blocksSyncedFloat * utils.OneHundred,
		Rate:          blocksPerSecondFloat,
		TimeRemaining: utils.TimeToTip(blocksPerSecondFloat, adjustedBlocks, tipIndex).String(),
	}
}

// CheckDataStatus contains both CheckDataStats
// and CheckDataProgress.
type CheckDataStatus struct {
	Stats    *CheckDataStats    `json:"stats"`
	Progress *CheckDataProgress `json:"progress"`
}

// ComputeCheckDataStatus returns a populated
// *CheckDataStatus.
func ComputeCheckDataStatus(
	ctx context.Context,
	counters *storage.CounterStorage,
	balances *storage.BalanceStorage,
	fetcher *fetcher.Fetcher,
	network *types.NetworkIdentifier,
) *CheckDataStatus {
	return &CheckDataStatus{
		Stats: ComputeCheckDataStats(
			ctx,
			counters,
			balances,
		),
		Progress: ComputeCheckDataProgress(
			ctx,
			fetcher,
			network,
			counters,
		),
	}
}

// FetchCheckDataStatus fetches *CheckDataStatus.
func FetchCheckDataStatus(url string) (*CheckDataStatus, error) {
	var status CheckDataStatus
	if err := JSONFetch(url, &status); err != nil {
		return nil, fmt.Errorf("%w: unable to fetch construction status", err)
	}

	return &status, nil
}

// CheckDataTests indicates which tests passed.
// If a test is nil, it did not apply to the run.
//
// TODO: add CoinTracking
type CheckDataTests struct {
	RequestResponse   bool  `json:"request_response"`
	ResponseAssertion bool  `json:"response_assertion"`
	BlockSyncing      *bool `json:"block_syncing"`
	BalanceTracking   *bool `json:"balance_tracking"`
	Reconciliation    *bool `json:"reconciliation"`
}

// convertBool converts a *bool
// to a test result.
func convertBool(v *bool) string {
	if v == nil {
		return "NOT TESTED"
	}

	if *v {
		return "PASSED"
	}

	return "FAILED"
}

// Print logs CheckDataTests to the console.
func (c *CheckDataTests) Print() {
	table := tablewriter.NewWriter(os.Stdout)
	table.SetRowLine(true)
	table.SetRowSeparator("-")
	table.SetHeader([]string{"check:data Tests", "Description", "Status"})
	table.Append(
		[]string{
			"Request/Response",
			"Rosetta implementation serviced all requests",
			convertBool(&c.RequestResponse),
		},
	)
	table.Append(
		[]string{
			"Response Assertion",
			"All responses are correctly formatted",
			convertBool(&c.ResponseAssertion),
		},
	)
	table.Append(
		[]string{
			"Block Syncing",
			"Blocks are connected into a single canonical chain",
			convertBool(c.BlockSyncing),
		},
	)
	table.Append(
		[]string{
			"Balance Tracking",
			"Account balances did not go negative",
			convertBool(c.BalanceTracking),
		},
	)
	table.Append(
		[]string{
			"Reconciliation",
			"No balance discrepencies were found between computed and live balances",
			convertBool(c.Reconciliation),
		},
	)

	table.Render()
}

// RequestResponseTest returns a boolean
// indicating if all endpoints received
// a non-500 response.
func RequestResponseTest(err error) bool {
	return !(fetcher.Err(err) ||
		errors.Is(err, utils.ErrNetworkNotSupported) ||
		errors.Is(err, syncer.ErrGetNetworkStatusFailed) ||
		errors.Is(err, syncer.ErrFetchBlockFailed))
}

// ResponseAssertionTest returns a boolean
// indicating if all responses received from
// the server were correctly formatted.
func ResponseAssertionTest(err error) bool {
	is, _ := asserter.Err(err)
	return !is
}

// BlockSyncingTest returns a boolean
// indicating if it was possible to sync
// blocks.
func BlockSyncingTest(err error, blocksSynced bool) *bool {
	syncPass := true
	storageFailed, _ := storage.Err(err)
	if syncer.Err(err) ||
		(storageFailed && !errors.Is(err, storage.ErrNegativeBalance)) {
		syncPass = false
	}

	if !blocksSynced && syncPass {
		return nil
	}

	return &syncPass
}

// BalanceTrackingTest returns a boolean
// indicating if any balances went negative
// while syncing.
func BalanceTrackingTest(cfg *configuration.Configuration, err error, operationsSeen bool) *bool {
	balancePass := true
	for _, balanceStorageErr := range storage.BalanceStorageErrs {
		if errors.Is(err, balanceStorageErr) {
			balancePass = false
			break
		}
	}

	if (cfg.Data.BalanceTrackingDisabled || !operationsSeen) && balancePass {
		return nil
	}

	return &balancePass
}

// ReconciliationTest returns a boolean
// if no reconciliation errors were received.
func ReconciliationTest(
	cfg *configuration.Configuration,
	err error,
	reconciliationsPerformed bool,
) *bool {
	relatedErrors := []error{
		ErrReconciliationFailure,
	}
	reconciliationPass := true
	for _, relatedError := range relatedErrors {
		if errors.Is(err, relatedError) {
			reconciliationPass = false
			break
		}
	}

	if (cfg.Data.BalanceTrackingDisabled || cfg.Data.ReconciliationDisabled || cfg.Data.IgnoreReconciliationError ||
		!reconciliationsPerformed) &&
		reconciliationPass {
		return nil
	}

	return &reconciliationPass
}

// ComputeCheckDataTests returns a populated CheckDataTests.
func ComputeCheckDataTests(
	ctx context.Context,
	cfg *configuration.Configuration,
	err error,
	counterStorage *storage.CounterStorage,
) *CheckDataTests {
	operationsSeen := false
	reconciliationsPerformed := false
	blocksSynced := false
	if counterStorage != nil {
		blocks, err := counterStorage.Get(ctx, storage.BlockCounter)
		if err == nil && blocks.Int64() > 0 {
			blocksSynced = true
		}

		ops, err := counterStorage.Get(ctx, storage.OperationCounter)
		if err == nil && ops.Int64() > 0 {
			operationsSeen = true
		}

		activeReconciliations, err := counterStorage.Get(ctx, storage.ActiveReconciliationCounter)
		if err == nil && activeReconciliations.Int64() > 0 {
			reconciliationsPerformed = true
		}

		inactiveReconciliations, err := counterStorage.Get(
			ctx,
			storage.InactiveReconciliationCounter,
		)
		if err == nil && inactiveReconciliations.Int64() > 0 {
			reconciliationsPerformed = true
		}
	}

	return &CheckDataTests{
		RequestResponse:   RequestResponseTest(err),
		ResponseAssertion: ResponseAssertionTest(err),
		BlockSyncing:      BlockSyncingTest(err, blocksSynced),
		BalanceTracking:   BalanceTrackingTest(cfg, err, operationsSeen),
		Reconciliation:    ReconciliationTest(cfg, err, reconciliationsPerformed),
	}
}

// ComputeCheckDataResults returns a populated CheckDataResults.
func ComputeCheckDataResults(
	cfg *configuration.Configuration,
	err error,
	counterStorage *storage.CounterStorage,
	balanceStorage *storage.BalanceStorage,
	endCondition configuration.CheckDataEndCondition,
	endConditionDetail string,
) *CheckDataResults {
	ctx := context.Background()
	tests := ComputeCheckDataTests(ctx, cfg, err, counterStorage)
	stats := ComputeCheckDataStats(ctx, counterStorage, balanceStorage)
	results := &CheckDataResults{
		Tests: tests,
		Stats: stats,
	}

	if err != nil {
		results.Error = err.Error()

		// If all tests pass, but we still encountered an error,
		// then we hard exit without showing check:data results
		// because the error falls beyond our test coverage.
		if tests.RequestResponse &&
			tests.ResponseAssertion &&
			(tests.BlockSyncing == nil || *tests.BlockSyncing) &&
			(tests.BalanceTracking == nil || *tests.BalanceTracking) &&
			(tests.Reconciliation == nil || *tests.Reconciliation) {
			results.Tests = nil
		}

		// We never want to populate an end condition
		// if there was an error!
		return results
	}

	if len(endCondition) > 0 {
		results.EndCondition = &EndCondition{
			Type:   endCondition,
			Detail: endConditionDetail,
		}
	}

	return results
}

// ExitData exits check:data, logs the test results to the console,
// and to a provided output path.
func ExitData(
	config *configuration.Configuration,
	counterStorage *storage.CounterStorage,
	balanceStorage *storage.BalanceStorage,
	err error,
	endCondition configuration.CheckDataEndCondition,
	endConditionDetail string,
) error {
	results := ComputeCheckDataResults(
		config,
		err,
		counterStorage,
		balanceStorage,
		endCondition,
		endConditionDetail,
	)
	if results != nil {
		results.Print()
		results.Output(config.Data.ResultsOutputFile)
	}

	return err
}
