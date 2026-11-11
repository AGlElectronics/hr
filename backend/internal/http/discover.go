package http

import (
	"context"
	"errors"

	"github.com/rs/zerolog"

	"github.com/AGlElectronics/hr/backend/internal/catalog"
	"github.com/AGlElectronics/hr/backend/internal/denlink"
)

// discoverRoleCatalogs is the catalog fetcher's Discover hook. It asks Den
// which modules are paired (GET /api/v1/integrations/modules) and keeps
// those with a slug and a role catalog URL. Do not append
// /api/role-catalog: Den already did.
//
// ErrNotPaired means standalone: nothing is discovered, so
// ROLE_CATALOG_SOURCES is the whole list. Any other error is logged and
// also yields nothing for this attempt; HR still shows the last catalog
// it saved for a module.
func discoverRoleCatalogs(ctx context.Context, den *denlink.Client, log zerolog.Logger) []catalog.Source {
	mods, err := den.Modules(ctx)
	if err != nil {
		if !errors.Is(err, denlink.ErrNotPaired) {
			log.Warn().Err(err).Msg("den link: paired modules")
		}
		return nil
	}
	out := make([]catalog.Source, 0, len(mods))
	for _, m := range mods {
		if m.Slug == "" || m.RoleCatalogURL == "" {
			continue
		}
		out = append(out, catalog.Source{Slug: m.Slug, URL: m.RoleCatalogURL})
	}
	return out
}
