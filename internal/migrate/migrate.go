package migrate

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/xuekeven/weibo-assistant/internal/store"
)

func Apply(ctx context.Context, st *store.Store) error {
	files, err := filepath.Glob("migrations/*.sql")
	if err != nil {
		return err
	}
	sort.Strings(files)
	for _, file := range files {
		sqlBytes, err := os.ReadFile(file)
		if err != nil {
			return err
		}
		if err := st.Exec(ctx, string(sqlBytes)); err != nil {
			return fmt.Errorf("apply %s: %w", file, err)
		}
	}
	return nil
}
