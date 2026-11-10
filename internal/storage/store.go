package storage

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"pangolin-port-manager/internal/models"
)

var (
	ErrNotFound       = errors.New("port rule not found")
	ErrDuplicatePort  = errors.New("a rule with this port and protocol already exists")
)

type Store struct {
	mu       sync.RWMutex
	filePath string
	ports    map[string]models.PortRule
}

func NewStore(filePath string) (*Store, error) {
	s := &Store{
		filePath: filePath,
		ports:    make(map[string]models.PortRule),
	}

	if err := s.load(); err != nil {
		return nil, fmt.Errorf("failed to load ports store: %w", err)
	}

	return s, nil
}

func (s *Store) load() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, err := os.Stat(s.filePath); os.IsNotExist(err) {
		// File does not exist yet; create directory if needed
		dir := filepath.Dir(s.filePath)
		if err := os.MkdirAll(dir, 0755); err != nil {
			return err
		}
		return nil
	}

	data, err := os.ReadFile(s.filePath)
	if err != nil {
		return err
	}

	if len(data) == 0 {
		return nil
	}

	var list []models.PortRule
	if err := json.Unmarshal(data, &list); err != nil {
		return err
	}

	s.ports = make(map[string]models.PortRule, len(list))
	for _, rule := range list {
		s.ports[rule.ID] = rule
	}

	return nil
}

func (s *Store) saveLocked() error {
	dir := filepath.Dir(s.filePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	list := make([]models.PortRule, 0, len(s.ports))
	for _, rule := range s.ports {
		list = append(list, rule)
	}

	// Sort deterministically by port ascending, then protocol
	sort.Slice(list, func(i, j int) bool {
		if list[i].Port == list[j].Port {
			return list[i].Protocol < list[j].Protocol
		}
		return list[i].Port < list[j].Port
	})

	data, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}

	// Atomic write via temp file
	tmpFile := s.filePath + ".tmp"
	if err := os.WriteFile(tmpFile, data, 0644); err != nil {
		return err
	}

	return os.Rename(tmpFile, s.filePath)
}

func (s *Store) List() []models.PortRule {
	s.mu.RLock()
	defer s.mu.RUnlock()

	list := make([]models.PortRule, 0, len(s.ports))
	for _, rule := range s.ports {
		list = append(list, rule)
	}

	sort.Slice(list, func(i, j int) bool {
		if list[i].Port == list[j].Port {
			return list[i].Protocol < list[j].Protocol
		}
		return list[i].Port < list[j].Port
	})

	return list
}

func (s *Store) Get(id string) (*models.PortRule, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rule, ok := s.ports[id]
	if !ok {
		return nil, ErrNotFound
	}
	return &rule, nil
}

func (s *Store) Add(rule models.PortRule) (*models.PortRule, error) {
	if err := rule.Validate(); err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	for _, existing := range s.ports {
		if existing.Port == rule.Port && existing.Protocol == rule.Protocol {
			return nil, fmt.Errorf("%w: %d/%s (%s)", ErrDuplicatePort, rule.Port, rule.Protocol, existing.Name)
		}
	}

	now := time.Now().UTC()
	if rule.ID == "" {
		rule.ID = models.GenerateID()
	}
	rule.CreatedAt = now
	rule.UpdatedAt = now

	s.ports[rule.ID] = rule
	if err := s.saveLocked(); err != nil {
		delete(s.ports, rule.ID)
		return nil, fmt.Errorf("failed to persist port rule: %w", err)
	}

	return &rule, nil
}

func (s *Store) Update(rule models.PortRule) (*models.PortRule, error) {
	if err := rule.Validate(); err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	existing, ok := s.ports[rule.ID]
	if !ok {
		return nil, ErrNotFound
	}

	for id, other := range s.ports {
		if id != rule.ID && other.Port == rule.Port && other.Protocol == rule.Protocol {
			return nil, fmt.Errorf("%w: %d/%s (%s)", ErrDuplicatePort, rule.Port, rule.Protocol, other.Name)
		}
	}

	rule.CreatedAt = existing.CreatedAt
	rule.UpdatedAt = time.Now().UTC()

	s.ports[rule.ID] = rule
	if err := s.saveLocked(); err != nil {
		s.ports[rule.ID] = existing
		return nil, fmt.Errorf("failed to persist port rule update: %w", err)
	}

	return &rule, nil
}

func (s *Store) Toggle(id string) (*models.PortRule, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	rule, ok := s.ports[id]
	if !ok {
		return nil, ErrNotFound
	}

	rule.Enabled = !rule.Enabled
	rule.UpdatedAt = time.Now().UTC()

	s.ports[id] = rule
	if err := s.saveLocked(); err != nil {
		rule.Enabled = !rule.Enabled
		s.ports[id] = rule
		return nil, fmt.Errorf("failed to persist toggle: %w", err)
	}

	return &rule, nil
}

func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	existing, ok := s.ports[id]
	if !ok {
		return ErrNotFound
	}

	delete(s.ports, id)
	if err := s.saveLocked(); err != nil {
		s.ports[id] = existing
		return fmt.Errorf("failed to persist deletion: %w", err)
	}

	return nil
}
