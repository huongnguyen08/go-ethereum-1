// Copyright 2014 The go-ethereum Authors
// This file is part of go-ethereum.
//
// go-ethereum is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// go-ethereum is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU General Public License for more details.
//
// You should have received a copy of the GNU General Public License
// along with go-ethereum. If not, see <http://www.gnu.org/licenses/>.

// geth is the official command-line client for Ethereum.
package main

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"gopkg.in/urfave/cli.v1"

	"github.com/ethereumproject/ethash"
	"github.com/ethereumproject/go-ethereum/cmd/utils"
	"github.com/ethereumproject/go-ethereum/console"
	"github.com/ethereumproject/go-ethereum/core"
	"github.com/ethereumproject/go-ethereum/eth"
	"github.com/ethereumproject/go-ethereum/ethdb"
	"github.com/ethereumproject/go-ethereum/internal/debug"
	"github.com/ethereumproject/go-ethereum/logger"
	"github.com/ethereumproject/go-ethereum/logger/glog"
	"github.com/ethereumproject/go-ethereum/metrics"
	"github.com/ethereumproject/go-ethereum/node"
)

// Version is the application revision identifier. It can be set with the linker
// as in: go build -ldflags "-X main.Version="`git describe --tags`
var Version = "unknown"

func main() {
	app := cli.NewApp()
	app.Name = filepath.Base(os.Args[0])
	app.Version = Version
	app.Usage = "the go-ethereum command line interface"
	app.Action = geth
	app.HideVersion = true // we have a command to print the version

	app.Commands = []cli.Command{
		importCommand,
		exportCommand,
		upgradedbCommand,
		removedbCommand,
		dumpCommand,
		monitorCommand,
		accountCommand,
		walletCommand,
		consoleCommand,
		attachCommand,
		javascriptCommand,
		{
			Action: makedag,
			Name:   "makedag",
			Usage:  "generate ethash dag (for testing)",
			Description: `
The makedag command generates an ethash DAG in /tmp/dag.

This command exists to support the system testing project.
Regular users do not need to execute it.
`,
		},
		{
			Action: gpuinfo,
			Name:   "gpuinfo",
			Usage:  "gpuinfo",
			Description: `
Prints OpenCL device info for all found GPUs.
`,
		},
		{
			Action: gpubench,
			Name:   "gpubench",
			Usage:  "benchmark GPU",
			Description: `
Runs quick benchmark on first GPU found.
`,
		},
		{
			Action: version,
			Name:   "version",
			Usage:  "print ethereum version numbers",
			Description: `
The output of this command is supposed to be machine-readable.
`,
		},
		{
			Action: initGenesis,
			Name:   "init",
			Usage:  "bootstraps and initialises a new genesis block (JSON)",
			Description: `
The init command initialises a new genesis block and definition for the network.
This is a destructive action and changes the network in which you will be
participating.
`,
		},
	}

	app.Flags = []cli.Flag{
		utils.IdentityFlag,
		utils.UnlockedAccountFlag,
		utils.PasswordFileFlag,
		utils.BootnodesFlag,
		utils.DataDirFlag,
		utils.KeyStoreDirFlag,
		utils.BlockchainVersionFlag,
		utils.OlympicFlag,
		utils.FastSyncFlag,
		utils.CacheFlag,
		utils.LightKDFFlag,
		utils.JSpathFlag,
		utils.ListenPortFlag,
		utils.MaxPeersFlag,
		utils.MaxPendingPeersFlag,
		utils.EtherbaseFlag,
		utils.GasPriceFlag,
		utils.MinerThreadsFlag,
		utils.MiningEnabledFlag,
		utils.MiningGPUFlag,
		utils.AutoDAGFlag,
		utils.TargetGasLimitFlag,
		utils.NATFlag,
		utils.NatspecEnabledFlag,
		utils.NoDiscoverFlag,
		utils.NodeKeyFileFlag,
		utils.NodeKeyHexFlag,
		utils.RPCEnabledFlag,
		utils.RPCListenAddrFlag,
		utils.RPCPortFlag,
		utils.RPCApiFlag,
		utils.WSEnabledFlag,
		utils.WSListenAddrFlag,
		utils.WSPortFlag,
		utils.WSApiFlag,
		utils.WSAllowedOriginsFlag,
		utils.IPCDisabledFlag,
		utils.IPCApiFlag,
		utils.IPCPathFlag,
		utils.ExecFlag,
		utils.PreloadJSFlag,
		utils.WhisperEnabledFlag,
		utils.DevModeFlag,
		utils.TestNetFlag,
		utils.NetworkIdFlag,
		utils.RPCCORSDomainFlag,
		utils.MetricsFlag,
		utils.FakePoWFlag,
		utils.SolcPathFlag,
		utils.GpoMinGasPriceFlag,
		utils.GpoMaxGasPriceFlag,
		utils.GpoFullBlockRatioFlag,
		utils.GpobaseStepDownFlag,
		utils.GpobaseStepUpFlag,
		utils.GpobaseCorrectionFactorFlag,
		utils.ExtraDataFlag,
		utils.Unused1,
	}
	app.Flags = append(app.Flags, debug.Flags...)

	app.Before = func(ctx *cli.Context) error {
		runtime.GOMAXPROCS(runtime.NumCPU())
		if err := debug.Setup(ctx); err != nil {
			return err
		}

		if s := ctx.String("metrics"); s != "" {
			go metrics.Collect(s)
		}

		// This should be the only place where reporting is enabled
		// because it is not intended to run while testing.
		// In addition to this check, bad block reports are sent only
		// for chains with the main network genesis block and network id 1.
		eth.EnableBadBlockReporting = true

		utils.SetupNetwork(ctx)
		return nil
	}

	app.After = func(ctx *cli.Context) error {
		logger.Flush()
		debug.Exit()
		console.Stdin.Close() // Resets terminal mode.
		return nil
	}

	if err := app.Run(os.Args); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// geth is the main entry point into the system if no special subcommand is ran.
// It creates a default node based on the command line arguments and runs it in
// blocking mode, waiting for it to be shut down.
func geth(ctx *cli.Context) error {
	node := utils.MakeSystemNode(Version, ctx)
	startNode(ctx, node)
	node.Wait()

	return nil
}

// initGenesis will initialise the given JSON format genesis file and writes it as
// the zero'd block (i.e. genesis) or will fail hard if it can't succeed.
func initGenesis(ctx *cli.Context) error {
	genesisPath := ctx.Args().First()
	if len(genesisPath) == 0 {
		utils.Fatalf("must supply path to genesis JSON file")
	}

	chainDb, err := ethdb.NewLDBDatabase(filepath.Join(utils.MustMakeDataDir(ctx), "chaindata"), 0, 0)
	if err != nil {
		utils.Fatalf("could not open database: %v", err)
	}

	genesisFile, err := os.Open(genesisPath)
	if err != nil {
		utils.Fatalf("failed to read genesis file: %v", err)
	}

	block, err := core.WriteGenesisBlock(chainDb, genesisFile)
	if err != nil {
		utils.Fatalf("failed to write genesis block: %v", err)
	}
	glog.V(logger.Info).Infof("successfully wrote genesis block and/or chain rule set: %x", block.Hash())
	return nil
}

// startNode boots up the system node and all registered protocols, after which
// it unlocks any requested accounts, and starts the RPC/IPC interfaces and the
// miner.
func startNode(ctx *cli.Context, stack *node.Node) {
	// Start up the node itself
	utils.StartNode(stack)

	// Unlock any account specifically requested
	var ethereum *eth.Ethereum
	if err := stack.Service(&ethereum); err != nil {
		utils.Fatalf("ethereum service not running: %v", err)
	}
	accman := ethereum.AccountManager()
	passwords := utils.MakePasswordList(ctx)

	accounts := strings.Split(ctx.GlobalString(utils.UnlockedAccountFlag.Name), ",")
	for i, account := range accounts {
		if trimmed := strings.TrimSpace(account); trimmed != "" {
			unlockAccount(ctx, accman, trimmed, i, passwords)
		}
	}
	// Start auxiliary services if enabled
	if ctx.GlobalBool(utils.MiningEnabledFlag.Name) {
		if err := ethereum.StartMining(ctx.GlobalInt(utils.MinerThreadsFlag.Name), ctx.GlobalString(utils.MiningGPUFlag.Name)); err != nil {
			utils.Fatalf("Failed to start mining: %v", err)
		}
	}
}

func makedag(ctx *cli.Context) error {
	args := ctx.Args()
	wrongArgs := func() {
		utils.Fatalf(`Usage: geth makedag <block number> <outputdir>`)
	}
	switch {
	case len(args) == 2:
		blockNum, err := strconv.ParseUint(args[0], 0, 64)
		dir := args[1]
		if err != nil {
			wrongArgs()
		} else {
			dir = filepath.Clean(dir)
			// seems to require a trailing slash
			if !strings.HasSuffix(dir, "/") {
				dir = dir + "/"
			}
			_, err = ioutil.ReadDir(dir)
			if err != nil {
				utils.Fatalf("Can't find dir")
			}
			fmt.Println("making DAG, this could take awhile...")
			ethash.MakeDAG(blockNum, dir)
		}
	default:
		wrongArgs()
	}
	return nil
}

func gpuinfo(ctx *cli.Context) error {
	eth.PrintOpenCLDevices()
	return nil
}

func gpubench(ctx *cli.Context) error {
	args := ctx.Args()
	wrongArgs := func() {
		utils.Fatalf(`Usage: geth gpubench <gpu number>`)
	}
	switch {
	case len(args) == 1:
		n, err := strconv.ParseUint(args[0], 0, 64)
		if err != nil {
			wrongArgs()
		}
		eth.GPUBench(n)
	case len(args) == 0:
		eth.GPUBench(0)
	default:
		wrongArgs()
	}
	return nil
}

func version(c *cli.Context) error {
	fmt.Println("Geth")
	fmt.Println("Version:", Version)
	fmt.Println("Protocol Versions:", eth.ProtocolVersions)
	fmt.Println("Network Id:", c.GlobalInt(utils.NetworkIdFlag.Name))
	fmt.Println("Go Version:", runtime.Version())
	fmt.Println("OS:", runtime.GOOS)
	fmt.Printf("GOPATH=%s\n", os.Getenv("GOPATH"))
	fmt.Printf("GOROOT=%s\n", runtime.GOROOT())

	return nil
}
