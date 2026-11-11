package main

import (
	"fmt"
	"os"
	"strconv"
	"time"

	mcobra "github.com/muesli/mango-cobra"
	"github.com/muesli/roff"
	"github.com/spf13/cobra"
)

const sourceDateEpoch = "SOURCE_DATE_EPOCH"

type datedDocument struct {
	*roff.Document
	date time.Time
}

func (d datedDocument) Heading(section uint, title, description string, _ time.Time) {
	d.Document.Heading(section, title, description, d.date)
}

func manDate(getenv func(string) string, now func() time.Time) (time.Time, error) {
	raw := getenv(sourceDateEpoch)
	if raw == "" {
		return now().UTC(), nil
	}
	secs, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || secs < 0 {
		return time.Time{}, fmt.Errorf("invalid %s %q: want non-negative unix seconds", sourceDateEpoch, raw)
	}
	return time.Unix(secs, 0).UTC(), nil
}

func manCommand() *cobra.Command {
	return &cobra.Command{
		Use:                   "man",
		Short:                 "Generates manpages",
		SilenceUsage:          true,
		DisableFlagsInUseLine: true,
		Hidden:                true,
		Args:                  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			date, err := manDate(os.Getenv, time.Now)
			if err != nil {
				return err
			}
			page, err := mcobra.NewManPage(1, cmd.Root())
			if err != nil {
				return err
			}
			_, err = fmt.Fprint(cmd.OutOrStdout(), page.Build(datedDocument{Document: roff.NewDocument(), date: date}))
			return err
		},
	}
}
