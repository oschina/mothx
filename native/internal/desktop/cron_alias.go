package desktop

import (
	"github.com/egoist/mygo/ui"

	"github.com/oschina/mothx/native/internal/acp"
	"github.com/oschina/mothx/native/internal/feature/cron"
)

type cronJobView = cron.JobView
type cronListView = cron.ListView
type cronJobDraft = cron.JobDraft

const cronModeYolo = cron.ModeYolo

func cronJobDisplayName(job cron.JobView) string   { return cron.JobDisplayName(job) }
func cronJobScheduleLabel(job cron.JobView) string { return cron.JobScheduleLabel(job) }
func cronRunStatusLabel(status string) string      { return cron.RunStatusLabel(status) }
func cronJobMeta(job cron.JobView) string          { return cron.JobMeta(job) }
func validateCronDraft(draft *cron.JobDraft) error { return cron.ValidateDraft(draft) }
func buildCronCreateRequest(draft *cron.JobDraft) (map[string]any, error) {
	return cron.BuildCreateRequest(draft)
}
func buildCronUpdateRequest(draft *cron.JobDraft) (map[string]any, error) {
	return cron.BuildUpdateRequest(draft)
}
func buildCronToggleRequest(id string, enabled bool) (map[string]any, error) {
	return cron.BuildToggleRequest(id, enabled)
}
func buildCronRemoveRequest(id string) (map[string]any, error) { return cron.BuildRemoveRequest(id) }
func buildCronRunRequest(id string) (map[string]any, error)    { return cron.BuildRunRequest(id) }
func cronToggleStatus(name string, enabled bool) string        { return cron.ToggleStatus(name, enabled) }

func (a *nativeApp) cronCapable() bool { return a.cron.Capable(a) }
func (a *nativeApp) cronBusy() bool    { return a.cron.Busy() }
func (a *nativeApp) cronPanelCurrent(seq, reloadSeq int64) bool {
	return a.cron.Current(a, seq, reloadSeq)
}
func (a *nativeApp) invalidateCronPanel()                  { a.cron.Invalidate() }
func (a *nativeApp) startCronPanelLoad()                   { a.cron.StartLoad(a) }
func (a *nativeApp) startCronPanelRefresh()                { a.cron.StartRefresh(a) }
func (a *nativeApp) applyCronListView(view *cron.ListView) { a.cron.ApplyListView(view) }
func (a *nativeApp) openCronNewDraft()                     { a.cron.OpenNewDraft() }
func (a *nativeApp) openCronEditDraft(index int)           { a.cron.OpenEditDraft(index) }
func (a *nativeApp) closeCronEditor()                      { a.cron.CloseEditor() }
func (a *nativeApp) saveCronDraft()                        { a.cron.Save(a) }
func (a *nativeApp) toggleCronJob(index int)               { a.cron.Toggle(a, index) }
func (a *nativeApp) runCronJob(index int)                  { a.cron.Run(a, index) }
func (a *nativeApp) requestDeleteCron(index int)           { a.cron.RequestDelete(index) }
func (a *nativeApp) confirmDeleteCron()                    { a.cron.ConfirmDelete(a) }
func (a *nativeApp) handleCronCompletedEvent(ev acp.SessionEvent) {
	a.cron.HandleCompletedEvent(a, ev)
}
func (a *nativeApp) cronErrorCard(c *ui.Context, p palette, message string) {
	a.cron.ErrCard(a, c, p, message)
}

func (a *nativeApp) cronStatusBanner(c *ui.Context, p palette) {
	a.cron.StatusBanner(c, p)
}

func (a *nativeApp) cronJobRow(c *ui.Context, p palette, index int) {
	a.cron.JobRow(a, c, p, index)
}

func (a *nativeApp) settingsCronPane(c *ui.Context, p palette)    { a.cron.View(a, c, p) }
func (a *nativeApp) settingsCronDialogs(c *ui.Context, p palette) { a.cron.Dialogs(a, c, p) }
