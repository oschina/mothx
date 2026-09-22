package main

import (
	"fmt"

	"github.com/oschina/mothx/internal/config"
	"github.com/oschina/mothx/internal/cron"
)

// openCronStore opens the shared SQLite cron store.
func openCronStore() cron.CronStore {
	settings, err := config.LoadSettings()
	if err != nil {
		return cron.NewSQLiteCronStore("")
	}
	return cron.NewSQLiteCronStore(settings.GetSessionDir())
}

// setCronEnabled enables or disables a cron job by ID.
func setCronEnabled(id string, enabled bool) error {
	store := openCronStore()
	job, err := store.Get(id)
	if err != nil {
		return err
	}
	job.Enabled = enabled
	if err := store.Update(*job); err != nil {
		return err
	}
	state := "enabled"
	if !enabled {
		state = "disabled"
	}
	fmt.Printf("✅ %s: [%s] %s\n", state, job.ID, job.Name)
	return nil
}
