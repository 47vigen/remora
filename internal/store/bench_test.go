package store

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/47vigen/remora/internal/alias"
)

func benchStore(b *testing.B) (*Store, []string) {
	ctx := context.Background()
	s, err := Open(ctx, filepath.Join(b.TempDir(), "remora.db"))
	if err != nil {
		b.Fatal(err)
	}
	sl, _ := NewSealer(bytes.Repeat([]byte{1}, 32))
	s.SetSealer(sl)
	var vals []string
	err = s.Write(ctx, func(tx *Tx) error {
		for i := range 10 {
			v := fmt.Sprintf("rk-%032d", i)
			k, err := tx.PutClientKey(ctx, ClientKey{Name: fmt.Sprint("k", i), Allow: []string{"*"}, Enabled: true}, v)
			if err != nil {
				return err
			}
			if err := tx.SetClientKeyVK(ctx, k.ID, "vk"+k.ID, "sk-bf-"+k.ID); err != nil {
				return err
			}
			vals = append(vals, v)
		}
		for i := range 20 {
			a := alias.Alias{Name: fmt.Sprint("alias-", i), Kind: alias.KindChat, Capabilities: []string{"tools", "structured_outputs"}}
			for j := range 4 {
				a.Members = append(a.Members, alias.Member{Ref: fmt.Sprintf("antigravity/model-%d", j)})
			}
			if err := tx.PutAlias(ctx, a); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = s.Close() })
	return s, vals
}

// perRequest is what proxy.authenticate + checkAliasKind do today.
func perRequest(ctx context.Context, s *Store, v string) {
	if _, err := s.ClientKeyByValue(ctx, v); err != nil {
		panic(err)
	}
	if _, err := s.Aliases(ctx); err != nil {
		panic(err)
	}
}

func BenchmarkKeyLookup(b *testing.B) {
	s, vals := benchStore(b)
	ctx := context.Background()
	b.ReportAllocs()
	for i := 0; b.Loop(); i++ {
		if _, err := s.ClientKeyByValue(ctx, vals[i%len(vals)]); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkAliases(b *testing.B) {
	s, _ := benchStore(b)
	ctx := context.Background()
	b.ReportAllocs()
	for b.Loop() {
		if _, err := s.Aliases(ctx); err != nil {
			b.Fatal(err)
		}
	}
}

func benchParallel(b *testing.B, par int) {
	s, vals := benchStore(b)
	ctx := context.Background()
	b.SetParallelism(par)
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			perRequest(ctx, s, vals[i%len(vals)])
			i++
		}
	})
	st := s.DB().Stats()
	b.ReportMetric(float64(st.OpenConnections), "openconns")
	b.ReportMetric(float64(st.MaxIdleClosed)/float64(b.N), "conncloses/op")
}

// BenchmarkPerRequestParallel is what authenticate and checkAliasKind cost per request, 32 goroutines per core.
func BenchmarkPerRequestParallel(b *testing.B) { benchParallel(b, 32) }
