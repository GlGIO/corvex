package run

// Source says which file the snapshot in a View came from.
type Source string

const (
	// SourceIndex: only the global index knew about this run — its repository
	// record is gone, unreadable, or older than the index line.
	SourceIndex Source = "index"
	// SourceRecord: the repository's record was fresher and won.
	SourceRecord Source = "record"
)

// View is one row of a listing: the freshest snapshot plus the liveness a
// reader could conclude from it.
type View struct {
	Record   Record   `json:"record"`
	Liveness Liveness `json:"liveness"`
	Source   Source   `json:"source"`
}

// Alive reports whether this run should be shown as still going.
func (v View) Alive() bool { return v.Liveness == LivenessAlive }

// List is the cross-repository answer to "what is running", readable by any
// second process: it consolidates the global index, then overlays each run's
// repository record when that record is fresher.
//
// The overlay is what makes liveness work at all. The index gets a line at
// start and at each status change, while the heartbeat only refreshes the local
// record — so judging freshness from the index alone would mark every healthy
// long run stale after StaleAfter. A missing or unreadable local record is not
// an error: the index line is then the best available truth (and a repository
// that was deleted is exactly the case where the pid probe reports dead).
func (r Resolver) List() ([]View, error) {
	home, err := r.homeDir()
	if err != nil {
		return nil, err
	}
	entries, err := ReadIndex(home)
	if err != nil {
		return nil, err
	}
	recs := ConsolidateIndex(entries)
	views := make([]View, 0, len(recs))
	for _, rec := range recs {
		src := SourceIndex
		if local, err := ReadRecord(rec.Repo, rec.RunID); err == nil && local.RunID == rec.RunID {
			if local.Freshness().After(rec.Freshness()) {
				rec, src = local, SourceRecord
			}
		}
		views = append(views, View{Record: rec, Liveness: r.Liveness(rec), Source: src})
	}
	return views, nil
}

// ListRepo answers the same question scoped to one repository, straight from
// `.corvex/runs/` and without consulting the global index — so it still works
// on a machine whose index was deleted, and so a repository is self-describing.
func (r Resolver) ListRepo(repo string) ([]View, error) {
	recs, err := ReadRecords(repo)
	if err != nil {
		return nil, err
	}
	views := make([]View, 0, len(recs))
	for _, rec := range recs {
		views = append(views, View{Record: rec, Liveness: r.Liveness(rec), Source: SourceRecord})
	}
	return views, nil
}

// Get returns one run by id from the global index, overlaid the same way List
// does. The bool is false when the id is unknown.
func (r Resolver) Get(id string) (View, bool, error) {
	views, err := r.List()
	if err != nil {
		return View{}, false, err
	}
	for _, v := range views {
		if v.Record.RunID == id {
			return v, true, nil
		}
	}
	return View{}, false, nil
}

func (r Resolver) homeDir() (string, error) {
	if r.Home != "" {
		return r.Home, nil
	}
	return Home()
}
