package history

import (
	"encoding/json"
	"hjh/spikes/s01/internal/configsnapshot"
)

func (s *Store) SaveConfiguration(snapshot configsnapshot.Snapshot) error {
	data, err := json.Marshal(snapshot.Descriptor)
	if err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.Exec("INSERT OR IGNORE INTO configurations(id,descriptor) VALUES(?,?)", snapshot.ID, string(data))
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 0 {
		for _, file := range snapshot.Descriptor.Files {
			if _, err = tx.Exec("INSERT INTO config_assets(config_id,path,content) VALUES(?,?,?)", snapshot.ID, file.Path, snapshot.Assets[file.Path]); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

func (s *Store) Configuration(id string) (configsnapshot.Descriptor, error) {
	var descriptor configsnapshot.Descriptor
	var data string
	if err := s.db.QueryRow("SELECT descriptor FROM configurations WHERE id=?", id).Scan(&data); err != nil {
		return descriptor, err
	}
	err := json.Unmarshal([]byte(data), &descriptor)
	return descriptor, err
}

func (s *Store) ConfigurationAsset(id, path string) ([]byte, error) {
	var data []byte
	err := s.db.QueryRow("SELECT content FROM config_assets WHERE config_id=? AND path=?", id, path).Scan(&data)
	return data, err
}
