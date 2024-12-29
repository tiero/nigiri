package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/urfave/cli/v2"
	"github.com/vulpemventures/nigiri/internal/config"
)

var logs = cli.Command{
	Name:   "logs",
	Usage:  "show logs for a service",
	Action: logsAction,
	Flags: []cli.Flag{
		&cli.BoolFlag{
			Name:    "follow",
			Aliases: []string{"f"},
			Usage:   "follow log output",
			Value:   false,
		},
	},
}

func logsAction(ctx *cli.Context) error {
	if !ctx.Args().Present() {
		return errors.New("service name required")
	}

	serviceName := ctx.Args().First()
	datadir := ctx.String("datadir")
	follow := ctx.Bool("follow")

	// If service is chopsticks or chopsticks-liquid, read from our log file
	if serviceName == "chopsticks" || serviceName == "chopsticks-liquid" {
		logPath := filepath.Join(datadir, "chopsticks.log")
		file, err := os.Open(logPath)
		if err != nil {
			return fmt.Errorf("failed to open log file: %w", err)
		}
		defer file.Close()

		if follow {
			// Start at end of file
			if _, err := file.Seek(0, io.SeekEnd); err != nil {
				return fmt.Errorf("failed to seek to end of file: %w", err)
			}

			// Create a scanner to read new lines
			for {
				buffer := make([]byte, 1024)
				n, err := file.Read(buffer)
				if err != nil && err != io.EOF {
					return fmt.Errorf("failed to read log file: %w", err)
				}

				if n > 0 {
					fmt.Print(string(buffer[:n]))
				}

				if err == io.EOF {
					// Wait for more data
					file.Seek(0, io.SeekCurrent)
					continue
				}
			}
		} else {
			// Just print the file contents
			if _, err := io.Copy(os.Stdout, file); err != nil {
				return fmt.Errorf("failed to read log file: %w", err)
			}
		}

		return nil
	}

	// For other services, use docker-compose logs
	composePath := filepath.Join(datadir, config.DefaultCompose)
	args := []string{"logs"}
	if follow {
		args = append(args, "-f")
	}
	args = append(args, serviceName)

	bashCmd := runDockerCompose(composePath, args...)
	bashCmd.Stdout = os.Stdout
	bashCmd.Stderr = os.Stderr

	return bashCmd.Run()
}
