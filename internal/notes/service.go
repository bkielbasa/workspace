package notes

import (
	"context"
	"errors"

	"github.com/google/uuid"
)

var (
	ErrNotFound  = errors.New("note not found")
	ErrForbidden = errors.New("access denied")
)

type Service struct {
	repo   Repository
	broker *Broker
}

func NewService(repo Repository, broker *Broker) *Service {
	return &Service{
		repo:   repo,
		broker: broker,
	}
}

func (s *Service) Broker() *Broker {
	return s.broker
}

func (s *Service) CreateNote(ctx context.Context, userID uuid.UUID, n Note) (*Note, error) {
	n.UserID = userID
	created, err := s.repo.CreateNote(ctx, n)
	if err != nil {
		return nil, err
	}
	s.broker.Publish(Event{
		Type:   "note_created",
		NoteID: created.ID,
		UserID: userID,
	})
	return created, nil
}

func (s *Service) GetNote(ctx context.Context, userID uuid.UUID, id uuid.UUID) (*Note, error) {
	n, err := s.repo.GetNote(ctx, id)
	if err != nil {
		return nil, ErrNotFound
	}
	if n.UserID != userID && !n.IsFamilyShared {
		return nil, ErrNotFound
	}
	return n, nil
}

func (s *Service) ListNotes(ctx context.Context, userID uuid.UUID, archived bool, tag string) ([]Note, error) {
	return s.repo.ListNotes(ctx, userID, archived, tag)
}

func (s *Service) UpdateNote(ctx context.Context, userID uuid.UUID, isAdmin bool, n Note) (*Note, error) {
	existing, err := s.repo.GetNote(ctx, n.ID)
	if err != nil {
		return nil, ErrNotFound
	}

	if existing.UserID != userID && !existing.IsFamilyShared {
		return nil, ErrNotFound
	}

	if existing.IsFamilyShared != n.IsFamilyShared && existing.UserID != userID && !isAdmin {
		return nil, ErrForbidden
	}

	updated, err := s.repo.UpdateNote(ctx, n)
	if err != nil {
		return nil, err
	}
	s.broker.Publish(Event{
		Type:   "note_updated",
		NoteID: updated.ID,
		UserID: userID,
	})
	return updated, nil
}

func (s *Service) DeleteNote(ctx context.Context, userID uuid.UUID, isAdmin bool, id uuid.UUID) error {
	existing, err := s.repo.GetNote(ctx, id)
	if err != nil {
		return ErrNotFound
	}
	if existing.UserID != userID && !isAdmin {
		return ErrForbidden
	}
	if err := s.repo.DeleteNote(ctx, id); err != nil {
		return err
	}
	s.broker.Publish(Event{
		Type:   "note_deleted",
		NoteID: id,
		UserID: userID,
	})
	return nil
}

func (s *Service) AddItem(ctx context.Context, userID uuid.UUID, noteID uuid.UUID, content string) (*NoteItem, error) {
	_, err := s.GetNote(ctx, userID, noteID)
	if err != nil {
		return nil, err
	}

	item, err := s.repo.AddItem(ctx, NoteItem{
		NoteID:  noteID,
		Content: content,
	})
	if err != nil {
		return nil, err
	}
	s.broker.Publish(Event{
		Type:      "item_added",
		NoteID:    noteID,
		ItemID:    item.ID,
		Completed: item.Completed,
		UserID:    userID,
	})
	return item, nil
}

func (s *Service) ToggleItem(ctx context.Context, userID uuid.UUID, noteID, itemID uuid.UUID, completed bool) (*NoteItem, error) {
	_, err := s.GetNote(ctx, userID, noteID)
	if err != nil {
		return nil, err
	}

	item, err := s.repo.ToggleItem(ctx, itemID, completed)
	if err != nil {
		return nil, err
	}
	s.broker.Publish(Event{
		Type:      "item_toggled",
		NoteID:    noteID,
		ItemID:    item.ID,
		Completed: item.Completed,
		UserID:    userID,
	})
	return item, nil
}

func (s *Service) DeleteItem(ctx context.Context, userID uuid.UUID, noteID, itemID uuid.UUID) error {
	_, err := s.GetNote(ctx, userID, noteID)
	if err != nil {
		return err
	}

	if err := s.repo.DeleteItem(ctx, itemID); err != nil {
		return err
	}
	s.broker.Publish(Event{
		Type:   "item_deleted",
		NoteID: noteID,
		ItemID: itemID,
		UserID: userID,
	})
	return nil
}
