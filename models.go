package main

import (
	"net/http"
	"sort"
)

func (s *Server) handleModels(w http.ResponseWriter, r *http.Request) {
	if !s.authorized(r) {
		s.store.CountAuthFail()
		proxyError(w, http.StatusUnauthorized, "A valid proxy API key is required")
		return
	}
	type entry struct {
		ID      string
		OwnedBy string
	}
	seen := map[string]entry{}
	add := func(id string, owner string) {
		if id == "" {
			return
		}
		if _, ok := seen[id]; !ok {
			seen[id] = entry{ID: id, OwnedBy: owner}
		}
	}

	for _, u := range s.store.effectiveUpstreams() {
		if !u.Enabled {
			continue
		}
		for _, m := range u.Models {
			add(m, u.Name)
		}
		for alias, target := range u.Mappings {
			if target != "" {
				add(alias, u.Name)
			}
		}
		for _, st := range s.store.SnapshotUpstreams() {
			if st.ID != u.ID {
				continue
			}
			for _, m := range st.Models {
				add(m, u.Name)
			}
		}
	}

	ids := make([]string, 0, len(seen))
	for id := range seen {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	data := make([]any, 0, len(ids))
	for _, id := range ids {
		data = append(data, map[string]any{
			"id":       id,
			"object":   "model",
			"owned_by": seen[id].OwnedBy,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"object": "list",
		"data":   data,
	})
}
