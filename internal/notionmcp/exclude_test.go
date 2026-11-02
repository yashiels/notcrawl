package notionmcp

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/openclaw/notcrawl/internal/store"
)

const excludedTeam = "99999999-9999-4999-8999-999999999999"

func exclusionStore(t *testing.T) *store.Store {
	t.Helper()
	st := newCrawlStore(t)
	if err := st.UpsertTeam(context.Background(), store.Team{ID: excludedTeam, Name: "Excluded", Source: store.SourceDesktop}); err != nil {
		t.Fatal(err)
	}
	return st
}

func TestExcludedSweepAndQueryNeverFetchNewTeamPage(t *testing.T) {
	for _, discover := range []bool{true, false} {
		t.Run(map[bool]string{true: "discovery", false: "targeted"}[discover], func(t *testing.T) {
			st := exclusionStore(t)
			fixture := newCrawlFixture()
			fixture.filtered = []searchResult{{ID: crawlPageF}}
			fixture.sweep = []searchResult{{ID: crawlPageF}, {ID: crawlPageD}}
			opts := SyncOptions{Discover: discover, Exclude: []string{excludedTeam}, Queries: []string{"recent"}}
			summary, err := newCrawlClient(t, fixture).Sync(context.Background(), st, opts)
			if err != nil {
				t.Fatal(err)
			}
			if contains(fixture.fetchedIDs(), crawlPageF) || !contains(fixture.fetchedIDs(), crawlPageD) || summary.Excluded == 0 {
				t.Fatalf("excluded new page fetched or allowed result lost: fetched=%v summary=%+v", fixture.fetchedIDs(), summary)
			}
			for i := 0; i < len(fixture.searches); i += 2 {
				global, scoped := fixture.searches[i], fixture.searches[i+1]
				if scoped["teamspace_id"] != excludedTeam {
					t.Fatalf("missing teamspace filter: %v", scoped)
				}
				delete(scoped, "teamspace_id")
				if !reflect.DeepEqual(global, scoped) {
					t.Fatalf("search arguments differ: global=%v scoped=%v", global, scoped)
				}
			}
		})
	}
}

func TestExcludedSearchFailureDisablesSweepAndQuery(t *testing.T) {
	for _, discover := range []bool{true, false} {
		st := exclusionStore(t)
		fixture := newCrawlFixture()
		fixture.failFiltered = true
		summary, err := newCrawlClient(t, fixture).Sync(context.Background(), st, SyncOptions{Discover: discover, Queries: []string{"recent"}, Exclude: []string{excludedTeam}})
		if err != nil {
			t.Fatal(err)
		}
		if contains(fixture.fetchedIDs(), crawlPageF) || len(summary.Warnings) == 0 {
			t.Fatalf("unguarded search result fetched: fetched=%v summary=%+v", fixture.fetchedIDs(), summary)
		}
	}
}

func TestExcludedAncestorsAndUnknownReferencesNeverFetch(t *testing.T) {
	ctx := context.Background()
	st := exclusionStore(t)
	for _, page := range []store.Page{
		{ID: crawlPageA, Title: "A", ParentID: crawlPageB, ParentTable: "block", Alive: true, Source: store.SourceDesktop},
		{ID: crawlPageD, Title: "D", ParentID: excludedTeam, ParentTable: "team", Alive: true, Source: store.SourceDesktop},
		{ID: crawlPageG, Title: "G", ParentID: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", ParentTable: "team", Alive: true, Source: store.SourceDesktop},
	} {
		if err := st.UpsertPage(ctx, page); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.UpsertBlock(ctx, store.Block{ID: crawlPageB, ParentID: crawlDatabaseC, ParentTable: "collection", Type: "text", Alive: true, Source: store.SourceDesktop}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertCollection(ctx, store.Collection{ID: crawlDatabaseC, ParentID: excludedTeam, ParentTable: "team", Source: store.SourceDesktop}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertCollection(ctx, store.Collection{ID: crawlCollectionH, ParentID: crawlPageD, ParentTable: "block", Source: store.SourceDesktop}); err != nil {
		t.Fatal(err)
	}
	fixture := newCrawlFixture()
	fixture.sweep = nil
	fixture.failOnce = nil
	fixture.documents[crawlPageG] = crawlDocument{kind: "page", text: "Links https://notion.so/" + strings.ReplaceAll(crawlPageD, "-", "") + " https://notion.so/" + strings.ReplaceAll(crawlPageE, "-", "")}
	summary, err := newCrawlClient(t, fixture).Sync(ctx, st, SyncOptions{Discover: true, PageIDs: []string{crawlPageA, crawlPageE}, Exclude: []string{excludedTeam}})
	if err != nil {
		t.Fatal(err)
	}
	if got := fixture.fetchedIDs(); len(got) != 1 || got[0] != crawlPageG {
		t.Fatalf("excluded ancestor or unknown reference fetched: %v", got)
	}
	if summary.Excluded < 5 || !strings.Contains(strings.Join(summary.Warnings, " "), "excluded") {
		t.Fatalf("missing skips/warning: %+v", summary)
	}
	pages, err := st.Pages(ctx)
	if err != nil || len(pages) != 3 {
		t.Fatalf("archived pages changed: %d, %v", len(pages), err)
	}
}

func TestExcludeRejectsInvalidReferenceBeforeGateway(t *testing.T) {
	st := exclusionStore(t)
	_, err := (Client{}).Sync(context.Background(), st, SyncOptions{Exclude: []string{"invalid"}})
	if err == nil || !strings.Contains(err.Error(), "exclude") {
		t.Fatalf("invalid exclusion accepted: %v", err)
	}
}

func TestFilteredPageWithIncompleteStoredAncestryStaysExcluded(t *testing.T) {
	ctx := context.Background()
	st := exclusionStore(t)
	fixture := newCrawlFixture()
	fixture.sweep = []searchResult{{ID: crawlPageA}}
	fixture.filtered = fixture.sweep
	summary, err := newCrawlClient(t, fixture).Sync(ctx, st, SyncOptions{Discover: true, Exclude: []string{excludedTeam}})
	if err != nil {
		t.Fatal(err)
	}
	if contains(fixture.fetchedIDs(), crawlPageA) || summary.Excluded == 0 {
		t.Fatalf("filtered page requeued from archive: %v, %+v", fixture.fetchedIDs(), summary)
	}
}

func TestExcludePageURLAndUnsafeAncestry(t *testing.T) {
	ctx := context.Background()
	st := exclusionStore(t)
	fixture := newCrawlFixture()
	fixture.sweep = nil
	summary, err := newCrawlClient(t, fixture).Sync(ctx, st, SyncOptions{Discover: true, PageIDs: []string{crawlPageA}, Exclude: []string{"https://notion.so/A-11111111111111111111111111111111"}})
	if err != nil {
		t.Fatal(err)
	}
	if contains(fixture.fetchedIDs(), crawlPageA) || summary.Excluded == 0 {
		t.Fatalf("page URL exclusion bypassed: %v", fixture.fetchedIDs())
	}
	if len(fixture.searches) != 1 {
		t.Fatalf("stored page exclusion treated as a teamspace: %v", fixture.searches)
	}
	for _, shape := range []string{"cycle", "depth", "missing", "blank"} {
		t.Run(shape, func(t *testing.T) {
			e := &exclusions{ids: map[string]bool{excludedTeam: true}, parents: map[string]store.ParentRef{}, skipped: map[string]bool{}}
			switch shape {
			case "cycle":
				e.parents[crawlPageA] = store.ParentRef{ID: crawlPageB, Table: "block"}
				e.parents[crawlPageB] = store.ParentRef{ID: crawlPageA, Table: "page"}
			case "depth":
				e.parents[crawlPageA] = store.ParentRef{ID: "0", Table: "block"}
				for i := 0; i < 101; i++ {
					e.parents[fmt.Sprint(i)] = store.ParentRef{ID: fmt.Sprint(i + 1), Table: "block"}
				}
			case "missing":
				e.parents[crawlPageA] = store.ParentRef{ID: crawlPageB, Table: "block"}
			case "blank":
				e.parents[crawlPageA] = store.ParentRef{}
			}
			var summary Summary
			if !e.skip(pageCandidate{ID: crawlPageA}, &summary) {
				t.Fatal("unsafe ancestry allowed")
			}
		})
	}
}

func TestExcludedBlockParentSurvivesBlankPageParent(t *testing.T) {
	ctx := context.Background()
	st := exclusionStore(t)
	if err := st.UpsertBlock(ctx, store.Block{ID: crawlPageA, ParentID: excludedTeam, ParentTable: "team", Type: "page", Alive: true, Source: store.SourceDesktop}); err != nil {
		t.Fatal(err)
	}
	fixture := newCrawlFixture()
	fixture.sweep = []searchResult{{ID: crawlPageA}}
	summary, err := newCrawlClient(t, fixture).Sync(ctx, st, SyncOptions{Discover: true, Exclude: []string{excludedTeam}})
	if err != nil {
		t.Fatal(err)
	}
	if contains(fixture.fetchedIDs(), crawlPageA) || summary.Excluded == 0 {
		t.Fatalf("block ancestry ignored: %v", fixture.fetchedIDs())
	}
}

func TestPageExclusionRequiresStoredAncestryForNewSearchHits(t *testing.T) {
	st := exclusionStore(t)
	fixture := newCrawlFixture()
	_, err := newCrawlClient(t, fixture).Sync(context.Background(), st, SyncOptions{Discover: true, Queries: []string{"recent"}, Exclude: []string{crawlPageA}})
	if err != nil {
		t.Fatal(err)
	}
	if contains(fixture.fetchedIDs(), crawlPageF) {
		t.Fatal("unknown descendant of excluded page could be fetched")
	}
}
