package services

import (
	"context"
	"errors"
	"path/filepath"
)

// ErrNotFound is returned when a requested service is not in the catalog.
var ErrNotFound = errors.New("service not found")

// Store defines the persistent storage methods needed by Coordinator.
type Store interface {
	GetServiceState(ctx context.Context, id ID) (Record, error)
	ListServiceStates(ctx context.Context) ([]Record, error)
}

// ServiceService defines the read-only service listing and lookup interface.
type ServiceService interface {
	List(ctx context.Context) ([]View, error)
	Get(ctx context.Context, id ID) (View, error)
}

// Coordinator combines static service catalog definitions with stored dynamic state.
type Coordinator struct {
	catalog Catalog
	store   Store
	root    string
}

// NewCoordinator returns a new Coordinator for the given catalog, store, and devstack root.
func NewCoordinator(catalog Catalog, st Store, root string) *Coordinator {
	return &Coordinator{
		catalog: catalog,
		store:   st,
		root:    root,
	}
}

// List returns views for all catalog services merged with their recorded state.
func (c *Coordinator) List(ctx context.Context) ([]View, error) {
	var records map[ID]Record
	if c.store != nil {
		recs, err := c.store.ListServiceStates(ctx)
		if err != nil {
			return nil, err
		}
		records = make(map[ID]Record, len(recs))
		for _, r := range recs {
			records[r.ID] = r
		}
	}

	views := make([]View, 0, len(c.catalog.Services))
	for _, s := range c.catalog.Services {
		var rec Record
		var found bool
		if records != nil {
			rec, found = records[s.ID]
		}
		views = append(views, c.buildView(s, rec, found))
	}
	return views, nil
}

// Get returns the view for a single catalog service by ID.
func (c *Coordinator) Get(ctx context.Context, id ID) (View, error) {
	s, ok := c.catalog.Get(id)
	if !ok {
		return View{}, ErrNotFound
	}

	var rec Record
	var found bool
	if c.store != nil {
		r, err := c.store.GetServiceState(ctx, id)
		if err != nil {
			if !errors.Is(err, ErrNotFound) && err.Error() != "not found" {
				return View{}, err
			}
		} else {
			rec = r
			found = true
		}
	}

	return c.buildView(s, rec, found), nil
}

func (c *Coordinator) buildView(s Service, rec Record, found bool) View {
	state := StateNotInstalled
	active := false
	image := s.Image
	lastErr := ""

	if found {
		if rec.State != "" {
			state = rec.State
		}
		active = rec.Active
		if rec.Image != "" {
			image = rec.Image
		}
		lastErr = rec.LastError
	}

	var configPath, dataPath string
	if c.root != "" {
		configPath = filepath.Join(c.root, "services", string(s.ID), "config")
		dataPath = filepath.Join(c.root, "services", string(s.ID), "data")
	}

	return View{
		ID:          s.ID,
		DisplayName: s.DisplayName,
		Group:       s.Group,
		State:       state,
		Active:      active,
		Image:       image,
		Ports:       s.Ports,
		LastError:   lastErr,
		ConfigPath:  configPath,
		DataPath:    dataPath,
	}
}
