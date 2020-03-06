package cmd

import (
	"fmt"
	"os"
	"os/signal"
	"sync"
	"time"

	"github.com/cosmos/cosmos-sdk/client"
	"github.com/spf13/cobra"

	"github.com/spf13/viper"
	"github.com/tendermint/tendermint/libs/common"
	httpClient "github.com/tendermint/tendermint/rpc/client"

	cliContext "github.com/cosmos/cosmos-sdk/client/context"
	"github.com/maticnetwork/heimdall/app"
	"github.com/maticnetwork/heimdall/bridge/setu/broadcaster"
	"github.com/maticnetwork/heimdall/bridge/setu/listener"
	"github.com/maticnetwork/heimdall/bridge/setu/processor"
	"github.com/maticnetwork/heimdall/bridge/setu/queue"
	"github.com/maticnetwork/heimdall/bridge/setu/util"
	"github.com/maticnetwork/heimdall/helper"
)

const (
	waitDuration = 1 * time.Minute
	logLevel     = "log_level"
)

// GetStartCmd returns the start command to start bridge
func GetStartCmd() *cobra.Command {
	startCmd := &cobra.Command{
		Use:   "start",
		Short: "Start bridge server",
		Run: func(cmd *cobra.Command, args []string) {
			logger := util.Logger().With("module", "bridge")

			// create codec
			cdc := app.MakeCodec()
			app.MakePulp()
			// queue connector & http client
			_queueConnector := queue.NewQueueConnector(helper.GetConfig().AmqpURL)
			_queueConnector.StartWorker()

			_txBroadcaster := broadcaster.NewTxBroadcaster(cdc)
			_httpClient := httpClient.NewHTTP(helper.GetConfig().TendermintRPCUrl, "/websocket")

			// selected services to start
			services := []common.Service{}
			services = append(services,
				listener.NewListenerService(cdc, _queueConnector),
				processor.NewProcessorService(cdc, _queueConnector, _httpClient, _txBroadcaster),
			)

			// sync group
			var wg sync.WaitGroup

			// go routine to catch signal
			catchSignal := make(chan os.Signal, 1)
			signal.Notify(catchSignal, os.Interrupt)
			go func() {
				// sig is a ^C, handle it
				for range catchSignal {
					// stop processes
					logger.Info("Received stop signal - Stopping all services")

					for _, service := range services {
						logger.Info("Stopping all services")
						service.Stop()
					}

					// stop http client
					_httpClient.Stop()

					// stop db instance
					util.CloseBridgeDBInstance()

					// exit
					os.Exit(1)
				}
			}()

			// Start http client
			err := _httpClient.Start()
			if err != nil {
				panic(fmt.Sprintf("Error connecting to server %v", err))
			}

			// cli context
			cliCtx := cliContext.NewCLIContext().WithCodec(cdc)
			cliCtx.BroadcastMode = client.BroadcastAsync
			cliCtx.TrustNode = true

			// start bridge services only when node fully synced
			for {
				if !util.IsCatchingUp(cliCtx) {
					logger.Info("Node upto date, starting bridge services")
					break
				} else {
					logger.Info("Waiting for heimdall to be synced")
				}
				time.Sleep(waitDuration)
			}

			// strt all processes
			for _, service := range services {
				go func(serv common.Service) {
					defer wg.Done()
					// TODO handle error while starting service
					serv.Start()
					<-serv.Quit()
				}(service)
			}
			// wait for all processes
			wg.Add(len(services))
			wg.Wait()
		}}

	// log level
	startCmd.Flags().String(logLevel, "info", "Log level for bridge")
	viper.BindPFlag(logLevel, startCmd.Flags().Lookup(logLevel))

	startCmd.Flags().Bool("all", false, "start all bridge services")
	viper.BindPFlag("all", startCmd.Flags().Lookup("all"))

	startCmd.Flags().StringSlice("only", []string{}, "comma separated bridge services to start")
	viper.BindPFlag("only", startCmd.Flags().Lookup("only"))
	return startCmd
}

func init() {
	rootCmd.AddCommand(GetStartCmd())
}
