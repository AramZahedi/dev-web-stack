package databases

import (
	"context"
	"fmt"
	"sort"

	"github.com/AramZahedi/dev-web-stack/internal/model"
)

// ServiceImpact is the read-only preflight result shown before deactivating or
// uninstalling a database service.
type ServiceImpact struct {
	ServiceID   string                  `json:"service_id"`
	LinkedSites []model.DatabaseSiteRef `json:"linked_sites"`
	Backups     []model.DatabaseBackup  `json:"backups"`
	RunningJobs []model.DatabaseJob     `json:"running_jobs"`
}

// Impact reports linked sites, backups, and running jobs for a service.
func (m *Manager) Impact(ctx context.Context, serviceID string) (ServiceImpact, error) {
	serviceID = canonicalServiceID(serviceID)
	if _, err := m.serviceView(ctx, serviceID); err != nil {
		return ServiceImpact{}, err
	}
	links, err := m.deps.Store.ListDatabaseLinksByService(ctx, serviceID)
	if err != nil {
		return ServiceImpact{}, err
	}
	sites := make(map[string]model.DatabaseSiteRef)
	for _, link := range links {
		sites[link.SiteID] = model.DatabaseSiteRef{ID: link.SiteID, Name: link.SiteName}
	}
	linkedSites := make([]model.DatabaseSiteRef, 0, len(sites))
	for _, site := range sites {
		linkedSites = append(linkedSites, site)
	}
	sort.SliceStable(linkedSites, func(i, j int) bool { return linkedSites[i].Name < linkedSites[j].Name })

	backupRecords, err := m.deps.Store.ListDatabaseBackups(ctx, serviceID)
	if err != nil {
		return ServiceImpact{}, err
	}
	backups := make([]model.DatabaseBackup, 0, len(backupRecords))
	for _, record := range backupRecords {
		backups = append(backups, backupToView(record))
	}

	jobRecords, err := m.deps.Store.ListDatabaseJobs(ctx, serviceID)
	if err != nil {
		return ServiceImpact{}, err
	}
	running := make([]model.DatabaseJob, 0)
	for _, record := range jobRecords {
		if record.Status != string(model.JobQueued) && record.Status != string(model.JobRunning) {
			continue
		}
		running = append(running, recordToJob(record))
	}
	return ServiceImpact{
		ServiceID:   serviceID,
		LinkedSites: linkedSites,
		Backups:     backups,
		RunningJobs: running,
	}, nil
}

// DeleteServiceMetadata removes users, grants, backups, and jobs after an
// uninstall with delete-data succeeded. Links are removed by DeleteServiceLinks;
// audit rows remain durable.
func (m *Manager) DeleteServiceMetadata(ctx context.Context, serviceID string) error {
	serviceID = canonicalServiceID(serviceID)
	links, err := m.deps.Store.ListDatabaseLinksByService(ctx, serviceID)
	if err != nil {
		return err
	}
	if err := m.deps.Store.DeleteServiceMetadata(ctx, serviceID); err != nil {
		return err
	}
	m.audit(ctx, "database.service.metadata_deleted", serviceID, "", "service", serviceID, "ok", map[string]any{"removed_links": len(links)})
	m.emit(ctx, "", "database.service.metadata_deleted", fmt.Sprintf("database metadata removed for service %s", serviceID))
	if len(links) > 0 {
		m.emit(ctx, "", "database.link.changed", fmt.Sprintf("removed %d site links to service %s after uninstall", len(links), serviceID))
	}
	return nil
}
