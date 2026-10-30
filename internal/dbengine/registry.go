package dbengine

import "github.com/AramZahedi/dev-web-stack/internal/model"

type engineRegistry struct{}

// For resolves the concrete engine for a catalog service.
func (engineRegistry) For(engine model.DatabaseEngine) (Engine, error) {
	switch engine {
	case model.DatabaseMySQL:
		return NewMySQL(), nil
	case model.DatabaseMariaDB:
		return NewMariaDB(), nil
	case model.DatabasePostgreSQL:
		return NewPostgreSQL(), nil
	case model.DatabaseRedis:
		return NewRedis(), nil
	default:
		return nil, ErrUnsupported
	}
}

func init() {
	DefaultRegistry = func() Registry { return engineRegistry{} }
}
