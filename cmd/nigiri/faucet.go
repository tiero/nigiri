package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"strconv"

	"github.com/urfave/cli/v2"
)

var faucet = cli.Command{
	Name:      "faucet",
	Usage:     "generate and send bitcoin to given address",
	ArgsUsage: "<address> [amount] [asset]",
	Action:    faucetAction,
	Flags: []cli.Flag{
		&liquidFlag,
	},
}

func faucetAction(ctx *cli.Context) error {

	if isRunning, _ := nigiriState.GetBool("running"); !isRunning {
		return errors.New("nigiri is not running")
	}

	if ctx.NArg() < 1 || ctx.NArg() > 3 {
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

	// Build the faucet URL
	url := fmt.Sprintf("http://127.0.0.1:%s/faucet", portStr)

	// Get network from state for LN commands
	network, err := nigiriState.GetString("network")
	if err != nil {
		return err
	}

	address := ctx.Args().First()
	if address == "cln" {
		jsonOut, err := outputCommand("docker", "exec", "cln", "lightning-cli", "--network="+network, "newaddr")
		if err != nil {
			return err
		}

		address, err = getValueByKey(jsonOut, "bech32")
		if err != nil {
			return err
		}
	}

	if address == "lnd" {
		jsonOut, err := outputCommand("docker", "exec", "lnd", "lncli", "--network="+network, "newaddress", "p2wkh")
		if err != nil {
			return err
		}

		address, err = getValueByKey(jsonOut, "address")
		if err != nil {
			return err
		}
	}

	request := map[string]interface{}{
		"address": address,
	}

	if ctx.Args().Len() >= 2 {
		amountFloat, err := strconv.ParseFloat(ctx.Args().Get(1), 64)
		if err != nil {
			return fmt.Errorf("invalid amount: %v", err)
		}
		request["amount"] = amountFloat
	}

	if isLiquid && ctx.Args().Len() == 3 {
		request["asset"] = ctx.Args().Get(2)
	}

	payload, err := json.Marshal(request)
	if err != nil {
		return err
	}
	res, err := http.Post(url, "application/json", bytes.NewBuffer(payload))
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

	var dat map[string]string
	if err := json.Unmarshal([]byte(data), &dat); err != nil {
		return errors.New("internal error, please try again")
	}
	if dat["txId"] == "" {
		return errors.New("not successful")
	}
	fmt.Println("txId: " + dat["txId"])

	return nil
}

func getValueByKey(JSONobject []byte, key string) (string, error) {
	var data map[string]interface{}
	err := json.Unmarshal(JSONobject, &data)
	if err != nil {
		return "", err
	}
	return data[key].(string), nil
}

func outputCommand(name string, arg ...string) ([]byte, error) {
	cmd := exec.Command(name, arg...)
	b, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("name: %v, args: %v, err: %v", name, arg, err.Error())
	}
	return b, nil
}
