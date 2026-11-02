package notionmcp

import (
	"context"
	"fmt"
	"strings"

	"github.com/openclaw/notcrawl/internal/store"
)

type exclusions struct {
	requireAncestry bool
	ids             map[string]bool
	teams           []string
	parents         map[string]store.ParentRef
	skipped         map[string]bool
}

func loadExclusions(ctx context.Context, st *store.Store, refs []string) (*exclusions, error) {
	if len(refs) == 0 {
		return nil, nil
	}
	e := &exclusions{ids: map[string]bool{}, parents: map[string]store.ParentRef{}, skipped: map[string]bool{}}
	for _, ref := range refs {
		id := pageIDFromReference(strings.TrimSpace(ref))
		if id == "" {
			return nil, fmt.Errorf("invalid Notion exclude reference %q", ref)
		}
		e.ids[id] = true
	}
	blocks, err := st.BlockParents(ctx)
	if err != nil {
		return nil, err
	}
	collections, err := st.CollectionParents(ctx)
	if err != nil {
		return nil, err
	}
	for id, parent := range blocks {
		e.parents[id] = parent
	}
	for id, parent := range collections {
		if parent.ID != "" || e.parents[id].ID == "" {
			e.parents[id] = parent
		}
	}
	rows, err := st.DB().QueryContext(ctx, "select id, coalesce(parent_id, ''), coalesce(parent_table, '') from pages")
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id string
		var parent store.ParentRef
		if err := rows.Scan(&id, &parent.ID, &parent.Table); err != nil {
			rows.Close()
			return nil, err
		}
		if parent.ID != "" || e.parents[id].ID == "" {
			e.parents[id] = parent
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	teamIDs := map[string]bool{}
	rows, err = st.DB().QueryContext(ctx, "select id from teams")
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		teamIDs[id] = true
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	for _, parent := range e.parents {
		if parent.Table == "team" {
			teamIDs[parent.ID] = true
		}
	}
	for _, id := range sortedKeys(e.ids) {
		if !teamIDs[id] {
			e.requireAncestry = true
		}
		_, stored := e.parents[id]
		if teamIDs[id] || !stored {
			e.teams = append(e.teams, id)
		}
	}
	return e, nil
}

func (e *exclusions) skip(candidate pageCandidate, summary *Summary) bool {
	if e == nil {
		return false
	}
	id := candidate.ID
	seen := map[string]bool{}
	blocked := true
	for depth := 0; depth < 100; depth++ {
		if e.ids[id] || seen[id] {
			break
		}
		seen[id] = true
		parent, ok := e.parents[id]
		if !ok {
			blocked = depth != 0 || !candidate.SearchVerified || e.requireAncestry
			break
		}
		if e.ids[parent.ID] {
			break
		}
		if parent.ID == "" {
			blocked = depth != 0 || !candidate.SearchVerified || e.requireAncestry
			break
		}
		if parent.Table == "space" || parent.Table == "team" {
			blocked = false
			break
		}
		id = parent.ID
	}
	if blocked {
		e.recordSkip(candidate.ID, summary)
		if candidate.Explicit {
			summary.Warnings = append(summary.Warnings, fmt.Sprintf("Notion MCP excluded explicit page %s (excluded or unknown ancestry)", candidate.ID))
		}
	}
	return blocked
}

func (e *exclusions) recordSkip(id string, summary *Summary) {
	if !e.skipped[id] {
		e.skipped[id] = true
		summary.Excluded++
	}
}

func (g *gatewayClient) searchExcluding(ctx context.Context, tools notionToolset, query string, pageSize int, extra map[string]any, e *exclusions, summary *Summary) ([]searchResult, []string, error) {
	results, notices, err := g.searchPages(ctx, tools.Search, query, pageSize, extra)
	if err != nil || e == nil {
		return results, notices, err
	}
	blocked := map[string]bool{}
	for _, team := range e.teams {
		if len(schemaProperties(tools.SearchSchema)["teamspace_id"]) == 0 {
			return nil, nil, fmt.Errorf("excluded-teamspace search unavailable; search skipped")
		}
		arguments := map[string]any{"teamspace_id": team}
		for key, value := range extra {
			arguments[key] = value
		}
		filtered, warnings, err := g.searchPages(ctx, tools.Search, query, pageSize, arguments)
		if err != nil || len(warnings) > 0 {
			return nil, nil, fmt.Errorf("excluded-teamspace search failed or returned notices; search skipped")
		}
		for _, result := range filtered {
			id := pageIDFromReference(result.ID)
			blocked[id] = true
			e.ids[id] = true
		}
	}
	out := make([]searchResult, 0, len(results))
	for _, result := range results {
		id := pageIDFromReference(result.ID)
		if blocked[id] {
			e.recordSkip(id, summary)
			continue
		}
		out = append(out, result)
	}
	return out, notices, nil
}
