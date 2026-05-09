package main

import (
	"context"
	"fmt"
	"os"

	"github.com/malonaz/core/go/binary"
	"github.com/malonaz/core/go/logging"
)

var databases = []string{
	"sgpt",
}

func setup(ctx context.Context) error {
	loggingOpts := &logging.Opts{
		Level:  opts.Logging.Level,
		Format: logging.FormatRaw,
	}
	rawLogger, err := logging.NewLogger(loggingOpts)
	if err != nil {
		return fmt.Errorf("instantiating raw logger: %w", err)
	}

	env := os.Getenv("ENV")
	binPath := "postgres-migrator"
	if env == "" {
		binPath = "plz-out/bin/core/cmd/postgres-migrator/postgres-migrator"
	}
	for _, database := range databases {
		dir := "/etc/hinata/migrations"
		if env == "" {
			dir = "plz-out/gen/sgpt/migrations"
		}
		initializer := binary.MustNew(
			binPath,
			fmt.Sprintf("--logging.format=%s", opts.Logging.Format),
			fmt.Sprintf("--logging.field=binary:%s", database),
			"--mode", "init",
			"--target-namespace", database,
			"--dir", dir,
		).WithLogger(rawLogger).AsJob()
		migrator := binary.MustNew(
			binPath,
			fmt.Sprintf("--logging.format=%s", opts.Logging.Format),
			fmt.Sprintf("--logging.field=binary:%s", database),
			"--mode", "migrate",
			"--target-namespace", database,
			"--dir", dir,
		).WithLogger(rawLogger).AsJob()

		if err := initializer.Run(); err != nil {
			return fmt.Errorf("running %s db initializer: %w", database, err)
		}
		if err := migrator.Run(); err != nil {
			return fmt.Errorf("running %s db migrator: %w", database, err)
		}
	}
	return nil
}
