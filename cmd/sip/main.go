// Copyright 2023 LiveKit, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/urfave/cli/v3"

	"github.com/livekit/protocol/logger"

	"github.com/veloxvoip/sip/pkg/stats"

	"github.com/veloxvoip/sip/pkg/config"
	"github.com/veloxvoip/sip/pkg/errors"
	"github.com/veloxvoip/sip/pkg/service"
	sipkg "github.com/veloxvoip/sip/pkg/sip"
	"github.com/veloxvoip/sip/version"
)

func main() {
	cmd := &cli.Command{
		Name:        "SIP",
		Usage:       "SIP Server",
		Version:     version.Version,
		Description: "SIP connectivity for LiveKit",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:    "config",
				Usage:   "SIP Server yaml config file",
				Sources: cli.EnvVars("SIP_CONFIG_FILE"),
			},
			&cli.StringFlag{
				Name:    "config-body",
				Usage:   "SIP Server yaml config body",
				Sources: cli.EnvVars("SIP_CONFIG_BODY"),
			},
		},
		Action: runService,
	}

	if err := cmd.Run(context.Background(), os.Args); err != nil {
		fmt.Println(err)
	}
}

func runService(_ context.Context, c *cli.Command) error {
	conf, err := getConfig(c, true)
	if err != nil {
		return err
	}
	log := logger.GetLogger()

	stopChan := make(chan os.Signal, 1)
	signal.Notify(stopChan, syscall.SIGTERM, syscall.SIGQUIT)

	killChan := make(chan os.Signal, 1)
	signal.Notify(killChan, syscall.SIGINT)

	mon, err := stats.NewMonitor(conf)
	if err != nil {
		return err
	}

	// Create a simple auth client
	// TODO: Load trunk configurations from config file or database
	authClient := sipkg.NewSimpleAuthClient()

	// Example: Add a trunk configuration
	// This would normally be loaded from your config file
	authClient.AddTrunk(&sipkg.TrunkConfig{
		ID:          "default-trunk",
		Name:        "Default Trunk",
		AllowedFrom: []string{"*"}, // Allow all sources for testing
		Routes: []sipkg.RouteConfig{
			{
				Name:    "default-route",
				Pattern: "*",                       // Match all numbers
				ToURI:   "sip:gateway.example.com", // Replace with your gateway
			},
		},
	})

	sipsrv, err := sipkg.NewService(conf, mon, log, func(projectID string) sipkg.AuthClient { return authClient })
	if err != nil {
		return err
	}
	svc := service.NewService(conf, log, sipsrv.Stop, sipsrv.ActiveCalls, authClient, mon)
	sipsrv.SetHandler(svc)

	if err = sipsrv.Start(); err != nil {
		return err
	}

	go func() {
		select {
		case sig := <-stopChan:
			log.Infow("exit requested, finishing all SIP then shutting down", "signal", sig)
			svc.Stop(false)
		case sig := <-killChan:
			log.Infow("exit requested, stopping all SIP and shutting down", "signal", sig)
			svc.Stop(true)
		}
	}()

	return svc.Run()
}

func getConfig(c *cli.Command, initialize bool) (*config.Config, error) {
	configFile := c.String("config")
	configBody := c.String("config-body")
	if configBody == "" {
		if configFile == "" {
			return nil, errors.ErrNoConfig
		}
		content, err := os.ReadFile(configFile)
		if err != nil {
			return nil, err
		}
		configBody = string(content)
	}

	conf, err := config.NewConfig(configBody)
	if err != nil {
		return nil, err
	}

	if initialize {
		err = conf.Init()
		if err != nil {
			return nil, err
		}
	}

	return conf, nil
}
