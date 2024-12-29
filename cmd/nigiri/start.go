package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/urfave/cli/v2"
	"github.com/vulpemventures/nigiri/internal/chopsticks"
	"github.com/vulpemventures/nigiri/internal/config"
	"github.com/vulpemventures/nigiri/internal/docker"
)

var start = cli.Command{
	Name:   "start",
	Usage:  "start nigiri",
	Action: startAction,
	Flags: []cli.Flag{
		&liquidFlag,
		&lnFlag,
		&cli.BoolFlag{
			Name:  "ci",
			Usage: "runs in headless mode without esplora for continuous integration environments",
			Value: false,
		},
		&cli.IntFlag{
			Name:  "chopsticks-bitcoin-port",
			Usage: "port for the Bitcoin Chopsticks HTTP server",
			Value: 3000,
		},
		&cli.IntFlag{
			Name:  "chopsticks-liquid-port",
			Usage: "port for the Liquid Chopsticks HTTP server",
			Value: 3001,
		},
	},
}

func startAction(ctx *cli.Context) error {

	if isRunning, _ := nigiriState.GetBool("running"); isRunning {
		return errors.New("nigiri is already running, please stop it first")
	}

	isLiquid := ctx.Bool("liquid")
	isLN := ctx.Bool("ln")
	isCI := ctx.Bool("ci")
	datadir := ctx.String("datadir")
	bitcoinPort := ctx.Int("chopsticks-bitcoin-port")
	liquidPort := ctx.Int("chopsticks-liquid-port")
	composePath := filepath.Join(datadir, config.DefaultCompose)

	// spin up all the services in the compose file
	servicesToRun := []string{"esplora"}
	if isLiquid {
		//this will only run chopsticks & chopsticks-liquid and servives they depends on
		servicesToRun = append(servicesToRun, "esplora-liquid")
	}

	if isLN {
		// LND
		servicesToRun = append(servicesToRun, "tap")
		// Core Lightning Network
		servicesToRun = append(servicesToRun, "cln")
	}

	if isCI {
		//this will only run chopsticks and servives it depends on
		servicesToRun = []string{}
		if isLN {
			// LND
			servicesToRun = append(servicesToRun, "tap")
			// Core Lightning Network
			servicesToRun = append(servicesToRun, "cln")
		}
	}

	args := []string{"up", "-d"}
	args = append(args, servicesToRun...)

	bashCmd := runDockerCompose(composePath, args...)
	bashCmd.Stdout = os.Stdout
	bashCmd.Stderr = os.Stderr

	if err := bashCmd.Run(); err != nil {
		return err
	}

	// Wait for services to be ready
	if err := docker.WaitForService(composePath, "bitcoin"); err != nil {
		return fmt.Errorf("bitcoin service not ready: %w", err)
	}
	if isLiquid {
		if err := docker.WaitForService(composePath, "liquid"); err != nil {
			return fmt.Errorf("liquid service not ready: %w", err)
		}
	}

	// Start the HTTP proxy servers
	httpServer = chopsticks.New(
		strconv.Itoa(bitcoinPort),                              // Bitcoin server port
		strconv.Itoa(liquidPort),                               // Liquid server port
		composePath,                              // Docker compose path from nigiri state
		filepath.Join(datadir, "chopsticks.log"), // Log file path
	)
	if err := httpServer.Start(); err != nil {
		return fmt.Errorf("failed to start HTTP servers: %w", err)
	}

	if err := nigiriState.Set(map[string]string{
		"running":                 strconv.FormatBool(true),
		"ci":                      strconv.FormatBool(isCI),
		"ln":                      strconv.FormatBool(isLN),
		"liquid":                  strconv.FormatBool(isLiquid),
		"datadir":                 datadir,
		"network":                 "regtest",
		"chopsticks_bitcoin_port": strconv.Itoa(bitcoinPort),
		"chopsticks_liquid_port":  strconv.Itoa(liquidPort),
	}); err != nil {
		return err
	}

	services, err := docker.GetServices(composePath)
	if err != nil {
		return err
	}

	fmt.Println()
	fmt.Println("ENDPOINTS")

	for _, nameAndEndpoint := range services {
		fmt.Printf("%s\n", nameAndEndpoint)
	}

	return nil
}
