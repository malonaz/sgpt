package main

import (
	"context"
	"fmt"
	"os"

	"github.com/malonaz/core/go/binary"
	"github.com/malonaz/core/go/logging"
)

var databases = []string{
	"chat",
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
	binaryLoggingArgs := func(name string, args ...any) []string {
		return []string{
			fmt.Sprintf("--logging.format=%s", opts.Logging.Format),
			fmt.Sprintf("--logging.field=binary:%s", fmt.Sprintf(name, args...)),
		}
	}

	env := os.Getenv("ENV")
	if env != "" && false {
		resetDB := binary.MustNew(
			fmt.Sprintf("./resetdb"),
			binaryLoggingArgs("reset_db_job")...,
		).WithLogger(rawLogger).AsJob()
		if err := resetDB.RunAsJob(); err != nil {
			return fmt.Errorf("running db reset: %w", err)
		}
	}
	for _, database := range databases {
		var initializer *binary.Binary
		var migrator *binary.Binary
		if env == "" {
			// Run the initializer binaries.
			initializer = binary.MustNew(
				fmt.Sprintf("plz-out/bin/%s/migrations/initializer", database),
				binaryLoggingArgs("postgres_initializer_job_%s", database)...,
			).WithLogger(rawLogger).AsJob()
			migrator = binary.MustNew(
				fmt.Sprintf("plz-out/bin/%s/migrations/migrator", database),
				binaryLoggingArgs("postgres_migrator_job_%s", database)...,
			).WithLogger(rawLogger).AsJob()
		} else {
			initializer = binary.MustNew(
				fmt.Sprintf("%s-postgres-initializer", database),
				binaryLoggingArgs("postgres_initializer_job_%s", database)...,
			).WithLogger(rawLogger).AsJob()
			migrator = binary.MustNew(
				fmt.Sprintf("%s-postgres-migrator", database),
				binaryLoggingArgs("postgres_migrator_job_%s", database)...,
			).WithLogger(rawLogger).AsJob()
		}
		if err := initializer.RunAsJob(); err != nil {
			return fmt.Errorf("running %s db initializer: %w", database, err)
		}
		if err := migrator.RunAsJob(); err != nil {
			return fmt.Errorf("running %s db migrator: %w", database, err)
		}
	}
	return nil
}
