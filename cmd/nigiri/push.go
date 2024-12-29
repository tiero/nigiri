package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/urfave/cli/v2"
)

var push = cli.Command{
	Name:      "push",
	Usage:     "broadcast raw transaction",
	ArgsUsage: "<hex>",
	Action:    pushAction,
	Flags: []cli.Flag{
		&liquidFlag,
	},
}

func pushAction(ctx *cli.Context) error {

	if isRunning, _ := nigiriState.GetBool("running"); !isRunning {
		return errors.New("nigiri is not running")
	}

	if ctx.NArg() != 1 {
		return errors.New("wrong number of arguments")
	}

	isLiquid := ctx.Bool("liquid")

	// Get the correct port from nigiri state
	var portStr string
	var err error
	if isLiquid {
		portStr, err = nigiriState.GetString("chopsticks_liquid_port")
	} else {
		portStr, err = nigiriState.GetString("chopsticks_bitcoin_port")
	}
	if err != nil {
		return fmt.Errorf("failed to get chopsticks port from state: %w", err)
	}

	// Build the tx URL
	url := fmt.Sprintf("http://127.0.0.1:%s/tx", portStr)

	hex := []byte(ctx.Args().First())

	res, err := http.Post(url, "application/string", bytes.NewBuffer(hex))
	if err != nil {
		return err
	}
	data, err := io.ReadAll(res.Body)
	if err != nil {
		return err
	}
	if res.StatusCode != http.StatusOK {
		return errors.New(string(data))
	}

	if string(data) == "" {
		return errors.New("not successful")
	}
	fmt.Println("\ntxId: " + string(data))
	return nil
}
