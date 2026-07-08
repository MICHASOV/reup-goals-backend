package knowledge

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
)

type Store struct {
	dbx *sql.DB
}

func NewStore(dbx *sql.DB) *Store {
	return &Store{dbx: dbx}
}

func (s *Store) List(ctx context.Context, workspaceID int) ([]Block, error) {
	if err := s.ensureDefaultBlocks(ctx, workspaceID); err != nil {
		return nil, err
	}

	rows, err := s.dbx.QueryContext(ctx, `
		SELECT id, workspace_id, type, title, description, content, status, sort_order, created_at, updated_at
		FROM v2_knowledge_base_blocks
		WHERE workspace_id=$1 AND archived_at IS NULL
		ORDER BY sort_order ASC, id ASC
	`, workspaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	blocks := []Block{}
	for rows.Next() {
		block, err := scanBlock(rows)
		if err != nil {
			return nil, err
		}
		blocks = append(blocks, block)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := s.attachDocumentViews(ctx, workspaceID, blocks); err != nil {
		return nil, err
	}

	return blocks, nil
}

func (s *Store) Get(ctx context.Context, workspaceID int, blockID int) (Block, error) {
	if err := s.ensureDefaultBlocks(ctx, workspaceID); err != nil {
		return Block{}, err
	}

	row := s.dbx.QueryRowContext(ctx, `
		SELECT id, workspace_id, type, title, description, content, status, sort_order, created_at, updated_at
		FROM v2_knowledge_base_blocks
		WHERE workspace_id=$1 AND id=$2 AND archived_at IS NULL
	`, workspaceID, blockID)

	block, err := scanBlock(row)
	if err != nil {
		return Block{}, err
	}
	blocks := []Block{block}
	if err := s.attachDocumentViews(ctx, workspaceID, blocks); err != nil {
		return Block{}, err
	}
	return blocks[0], nil
}

func (s *Store) Update(ctx context.Context, workspaceID int, blockID int, content string, status string) (Block, error) {
	content = strings.TrimSpace(content)
	if content == "" {
		status = StatusEmpty
	} else if status == "" || status == StatusEmpty {
		status = StatusDraft
	}

	row := s.dbx.QueryRowContext(ctx, `
		UPDATE v2_knowledge_base_blocks
		SET content=$1, status=$2, updated_at=NOW()
		WHERE workspace_id=$3 AND id=$4 AND archived_at IS NULL
		RETURNING id, workspace_id, type, title, description, content, status, sort_order, created_at, updated_at
	`, content, status, workspaceID, blockID)

	return scanBlock(row)
}

func (s *Store) ensureDefaultBlocks(ctx context.Context, workspaceID int) error {
	ready, err := s.defaultBlocksReady(ctx, workspaceID)
	if err != nil {
		return err
	}
	if ready {
		return nil
	}

	tx, err := s.dbx.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock($1)`, workspaceID+1000000); err != nil {
		return err
	}

	for _, definition := range blockDefinitions {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO v2_knowledge_base_blocks (
				workspace_id, type, title, description, status, sort_order
			)
			VALUES ($1, $2, $3, $4, $5, $6)
			ON CONFLICT (workspace_id, type) DO UPDATE SET
				title=EXCLUDED.title,
				description=EXCLUDED.description,
				sort_order=EXCLUDED.sort_order,
				updated_at=v2_knowledge_base_blocks.updated_at
		`, workspaceID, definition.Type, definition.Title, definition.Description, StatusEmpty, definition.SortOrder); err != nil {
			return err
		}
	}

	return tx.Commit()
}

func (s *Store) defaultBlocksReady(ctx context.Context, workspaceID int) (bool, error) {
	rows, err := s.dbx.QueryContext(ctx, `
		SELECT type
		FROM v2_knowledge_base_blocks
		WHERE workspace_id=$1 AND archived_at IS NULL
	`, workspaceID)
	if err != nil {
		return false, err
	}
	defer rows.Close()

	existing := map[string]bool{}
	for rows.Next() {
		var blockType string
		if err := rows.Scan(&blockType); err != nil {
			return false, err
		}
		existing[blockType] = true
	}
	if err := rows.Err(); err != nil {
		return false, err
	}
	if len(existing) < len(blockDefinitions) {
		return false, nil
	}
	for _, definition := range blockDefinitions {
		if !existing[definition.Type] {
			return false, nil
		}
	}
	return true, nil
}

type blockScanner interface {
	Scan(dest ...any) error
}

func scanBlock(scanner blockScanner) (Block, error) {
	var block Block
	err := scanner.Scan(
		&block.ID,
		&block.WorkspaceID,
		&block.Type,
		&block.Title,
		&block.Description,
		&block.Content,
		&block.Status,
		&block.SortOrder,
		&block.CreatedAt,
		&block.UpdatedAt,
	)
	if err != nil {
		return Block{}, err
	}

	return block, nil
}

func (s *Store) attachDocumentViews(ctx context.Context, workspaceID int, blocks []Block) error {
	if len(blocks) == 0 {
		return nil
	}
	neededBlockTypes := map[string]bool{}
	for _, block := range blocks {
		neededBlockTypes[block.Type] = true
	}

	documentToBlock := map[string]string{}
	for _, definition := range documentDefinitions {
		if neededBlockTypes[definition.BlockType] {
			documentToBlock[definition.Type] = definition.BlockType
		}
	}
	if len(documentToBlock) == 0 {
		return nil
	}

	rows, err := s.dbx.QueryContext(ctx, `
		SELECT document_id, document_type, title, rendered_text, sections_json,
			source_entry_ids_json, composer_prompt_version, updated_at
		FROM v2_knowledge_document_views
		WHERE workspace_id=$1
	`, workspaceID)
	if err != nil {
		return err
	}
	defer rows.Close()

	viewsByBlockType := map[string]*DocumentView{}
	for rows.Next() {
		var view DocumentView
		var sectionsRaw []byte
		var sourceIDsRaw []byte
		if err := rows.Scan(
			&view.DocumentID,
			&view.DocumentType,
			&view.Title,
			&view.RenderedText,
			&sectionsRaw,
			&sourceIDsRaw,
			&view.PromptVersion,
			&view.UpdatedAt,
		); err != nil {
			return err
		}
		blockType, ok := documentToBlock[view.DocumentType]
		if !ok {
			continue
		}
		if len(sectionsRaw) > 0 {
			if err := json.Unmarshal(sectionsRaw, &view.Sections); err != nil {
				return err
			}
		}
		if len(sourceIDsRaw) > 0 {
			if err := json.Unmarshal(sourceIDsRaw, &view.SourceEntryIDs); err != nil {
				return err
			}
		}
		viewsByBlockType[blockType] = &view
	}
	if err := rows.Err(); err != nil {
		return err
	}

	for index := range blocks {
		if view, ok := viewsByBlockType[blocks[index].Type]; ok {
			blocks[index].View = view
		}
	}
	return nil
}
