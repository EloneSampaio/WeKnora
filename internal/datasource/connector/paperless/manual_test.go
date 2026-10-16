package paperless

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestManualClientFacetsFollowPagination(t *testing.T) {
	var server *httptest.Server
	server = paperlessServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/tags/":
			if r.URL.Query().Get("page") == "2" {
				writeJSON(t, w, listResponse[tag]{Results: []tag{{ID: 2, Name: "Second"}}})
				return
			}
			writeJSON(t, w, listResponse[tag]{Next: server.URL + "/api/tags/?page=2", Results: []tag{{ID: 1, Name: "First"}}})
		case "/api/document_types/":
			writeJSON(t, w, listResponse[documentType]{Results: []documentType{{ID: 3, Name: "Invoice"}}})
		case "/api/correspondents/":
			writeJSON(t, w, listResponse[correspondent]{Results: []correspondent{{ID: 4, Name: "ACME"}}})
		default:
			t.Errorf("unexpected request %s", r.URL.String())
		}
	})
	defer server.Close()

	manual, err := NewManualClient(makeConfig(server.URL))
	if err != nil {
		t.Fatal(err)
	}
	facets, err := manual.Facets(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(facets.Tags) != 2 || facets.Tags[1].ID != 2 || len(facets.DocumentTypes) != 1 || len(facets.Correspondents) != 1 {
		t.Fatalf("unexpected facets: %+v", facets)
	}
}

func TestManualClientRejectsCrossOriginPagination(t *testing.T) {
	server := paperlessServer(t, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, listResponse[tag]{Next: "https://attacker.example/api/tags/?page=2", Results: []tag{{ID: 1, Name: "Tag"}}})
	})
	defer server.Close()
	manual, err := NewManualClient(makeConfig(server.URL))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manual.Facets(context.Background()); err == nil || !strings.Contains(err.Error(), "different origin") {
		t.Fatalf("Facets error = %v, want cross-origin rejection", err)
	}
}

func TestManualClientDocumentsUsesOneFilteredPage(t *testing.T) {
	var server *httptest.Server
	server = paperlessServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/documents/" {
			t.Errorf("path = %q", r.URL.Path)
		}
		query := r.URL.Query()
		want := map[string]string{
			"query": "receipt #2026", "tags__id__all": "7,8", "document_type__id": "9",
			"correspondent__id": "10", "page": "3", "page_size": "100",
		}
		for key, expected := range want {
			if got := query.Get(key); got != expected {
				t.Errorf("%s = %q, want %q", key, got, expected)
			}
		}
		if query.Get("ordering") != "" {
			t.Errorf("unexpected extra paging behavior: %s", r.URL.RawQuery)
		}
		writeJSON(t, w, listResponse[document]{Count: 201, Next: server.URL + "/api/documents/?page=4", Results: []document{{
			ID: 42, Title: "Receipt", OriginalFileName: "receipt.pdf", Tags: []int{7, 8}, DocumentType: 9, Correspondent: 10,
		}}})
	})
	defer server.Close()
	manual, err := NewManualClient(makeConfig(server.URL))
	if err != nil {
		t.Fatal(err)
	}
	page, err := manual.Documents(context.Background(), ManualDocumentFilter{
		Query: " receipt #2026 ", Tags: []int{7, 8}, DocumentTypeID: 9, CorrespondentID: 10, Page: 3, PageSize: 1000,
	})
	if err != nil {
		t.Fatal(err)
	}
	if page.Count != 201 || page.Page != 3 || page.PageSize != 100 || !page.HasNext || len(page.Results) != 1 || page.Results[0].ID != 42 {
		t.Fatalf("unexpected page: %+v", page)
	}
}

func TestManualClientOriginalReadsMetadataAndBoundedOriginal(t *testing.T) {
	server := paperlessServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/documents/42/":
			writeJSON(t, w, document{ID: 42, Title: "Invoice", OriginalFileName: "../invoice.pdf"})
		case "/api/documents/42/download/":
			if r.URL.Query().Get("original") != "true" {
				t.Errorf("original query = %q", r.URL.RawQuery)
			}
			w.Header().Set("Content-Type", "application/pdf")
			_, _ = w.Write([]byte("pdf-bytes"))
		default:
			t.Errorf("unexpected request %s", r.URL.String())
		}
	})
	defer server.Close()
	manual, err := NewManualClient(makeConfig(server.URL))
	if err != nil {
		t.Fatal(err)
	}
	original, err := manual.Original(context.Background(), 42, 16)
	if err != nil {
		t.Fatal(err)
	}
	if original.ID != 42 || original.Filename != ".._invoice.pdf" || original.ContentType != "application/pdf" || string(original.Data) != "pdf-bytes" {
		t.Fatalf("unexpected original: %+v", original)
	}
}

func TestManualClientOriginalUsesSafeFallbackAndRejectsOversize(t *testing.T) {
	server := paperlessServer(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/documents/42/":
			writeJSON(t, w, document{ID: 42, Title: "Invoice / March"})
		case "/api/documents/42/download/":
			_, _ = w.Write([]byte("1234"))
		default:
			t.Errorf("unexpected request %s", r.URL.String())
		}
	})
	defer server.Close()
	manual, err := NewManualClient(makeConfig(server.URL))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manual.Original(context.Background(), 42, 3); err == nil || !strings.Contains(err.Error(), "exceeds maximum") {
		t.Fatalf("oversize error = %v", err)
	}
	original, err := manual.Original(context.Background(), 42, 10)
	if err != nil {
		t.Fatal(err)
	}
	if original.Filename != "Invoice _ March.pdf" {
		t.Fatalf("fallback filename = %q", original.Filename)
	}
	if _, err := manual.Original(context.Background(), 42, 0); err == nil {
		t.Fatal("expected non-positive max size error")
	}
}

func TestManualClientOriginalReportsMetadataAndDownloadFailures(t *testing.T) {
	for _, failedPath := range []string{"/api/documents/42/", "/api/documents/42/download/"} {
		t.Run(strings.Trim(failedPath, "/"), func(t *testing.T) {
			server := paperlessServer(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == failedPath {
					http.Error(w, "missing", http.StatusNotFound)
					return
				}
				if r.URL.Path == "/api/documents/42/" {
					writeJSON(t, w, document{ID: 42, Title: "Doc"})
					return
				}
				if r.URL.Path == "/api/documents/42/download/" {
					_, _ = fmt.Fprint(w, "data")
					return
				}
				t.Errorf("unexpected request %s", r.URL.String())
			})
			defer server.Close()
			manual, err := NewManualClient(makeConfig(server.URL))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := manual.Original(context.Background(), 42, 10); err == nil {
				t.Fatalf("Original(%q) returned no error", failedPath)
			}
		})
	}
}
