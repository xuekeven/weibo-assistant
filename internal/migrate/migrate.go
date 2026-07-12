package migrate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/xuekeven/weibo-assistant/internal/store"
)

const migrationLockID int64 = 824719306115

type migrationFile struct {
	Version  int64
	Name     string
	Path     string
	SQL      string
	Checksum string
}

func Apply(ctx context.Context, st *store.Store) error {
	files, err := loadMigrationFiles("migrations")
	if err != nil {
		return err
	}
	return st.WithTransaction(ctx, func(tx pgx.Tx) error {
		// advisory lock 防止 server 和 monitor 同时启动时重复执行迁移。
		if _, err := tx.Exec(ctx, `select pg_advisory_xact_lock($1)`, migrationLockID); err != nil {
			return fmt.Errorf("acquire migration lock: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			create table if not exists schema_migrations (
			  version bigint primary key,
			  name text not null,
			  checksum text not null,
			  applied_at timestamptz not null default now()
			)
		`); err != nil {
			return fmt.Errorf("create schema_migrations: %w", err)
		}

		applied, err := loadAppliedMigrations(ctx, tx)
		if err != nil {
			return err
		}
		for _, file := range files {
			if checksum, ok := applied[file.Version]; ok {
				// 已执行的迁移不能被静默修改，否则不同环境会得到不同的表结构。
				if checksum != file.Checksum {
					return fmt.Errorf("migration %03d checksum mismatch", file.Version)
				}
				continue
			}
			if _, err := tx.Exec(ctx, file.SQL); err != nil {
				return fmt.Errorf("apply %s: %w", file.Path, err)
			}
			if _, err := tx.Exec(ctx, `
				insert into schema_migrations (version, name, checksum)
				values ($1, $2, $3)
			`, file.Version, file.Name, file.Checksum); err != nil {
				return fmt.Errorf("record migration %s: %w", file.Path, err)
			}
		}
		return nil
	})
}

func loadAppliedMigrations(ctx context.Context, tx pgx.Tx) (map[int64]string, error) {
	rows, err := tx.Query(ctx, `select version, checksum from schema_migrations order by version`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	applied := make(map[int64]string)
	for rows.Next() {
		var version int64
		var checksum string
		if err := rows.Scan(&version, &checksum); err != nil {
			return nil, err
		}
		applied[version] = checksum
	}
	return applied, rows.Err()
}

func loadMigrationFiles(dir string) ([]migrationFile, error) {
	paths, err := filepath.Glob(filepath.Join(dir, "*.sql"))
	if err != nil {
		return nil, err
	}
	if len(paths) == 0 {
		return nil, fmt.Errorf("no migration files found in %s", dir)
	}
	sort.Strings(paths)
	files := make([]migrationFile, 0, len(paths))
	seen := make(map[int64]string)
	for _, path := range paths {
		base := filepath.Base(path)
		prefix, name, ok := strings.Cut(base, "_")
		if !ok || !strings.HasSuffix(name, ".sql") {
			return nil, fmt.Errorf("invalid migration filename %q; expected NNN_name.sql", base)
		}
		version, err := strconv.ParseInt(prefix, 10, 64)
		if err != nil || version <= 0 {
			return nil, fmt.Errorf("invalid migration version in %q", base)
		}
		if previous, exists := seen[version]; exists {
			return nil, fmt.Errorf("duplicate migration version %d in %s and %s", version, previous, base)
		}
		sqlBytes, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		if len(strings.TrimSpace(string(sqlBytes))) == 0 {
			return nil, errors.New("empty migration: " + base)
		}
		sum := sha256.Sum256(sqlBytes)
		seen[version] = base
		files = append(files, migrationFile{
			Version:  version,
			Name:     strings.TrimSuffix(name, ".sql"),
			Path:     path,
			SQL:      string(sqlBytes),
			Checksum: hex.EncodeToString(sum[:]),
		})
	}
	return files, nil
}
