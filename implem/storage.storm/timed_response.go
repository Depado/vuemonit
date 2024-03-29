package storage

import (
	"errors"
	"fmt"
	"time"

	"github.com/rs/xid"
	bolt "go.etcd.io/bbolt"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"

	"github.com/Depado/vuemonit/interactor"
	"github.com/Depado/vuemonit/models"
)

// Given an xid byte key and a protobuf TimedResponse, returns a new
// models.TimedReponse
func newTimedResponseFromProto(k []byte, p *TimedResponse) (*models.TimedResponse, error) {
	id, err := xid.FromBytes(k)
	if err != nil {
		return nil, fmt.Errorf("parse xid: %w", err)
	}
	return &models.TimedResponse{
		ID:     id,
		At:     id.Time(),
		Server: p.GetServer().AsDuration(),
		Total:  p.GetTotal().AsDuration(),
		Status: int(p.GetStatus()),
	}, nil
}

func (s StormStorage) GetTimedResponseRange(svc *models.Service, from, to time.Time) ([]*models.TimedResponse, error) {
	// Genreate an xid with the given time to get the prefix matching the from
	// date
	prefix := xid.NewWithTime(from).Bytes()[:4]
	if svc.ID == "" {
		return nil, fmt.Errorf("service has no ID: %w", interactor.ErrNotFound)
	}
	xtr := []*models.TimedResponse{}
	err := s.db.Bolt.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(bucketName)).Bucket([]byte(svc.ID))
		if b == nil {
			return interactor.ErrNotFound
		}
		c := b.Cursor()

		var ctr *models.TimedResponse
		for k, v := c.Seek(prefix); k != nil && (ctr == nil || ctr.At.Before(to)); k, v = c.Next() {
			t := &TimedResponse{}
			if err := proto.Unmarshal(v, t); err != nil {
				return fmt.Errorf("unable to unmarshal data: %w", err)
			}
			tr, err := newTimedResponseFromProto(k, t)
			if err != nil {
				return fmt.Errorf("unable to parse timed response: %w", err)
			}
			tr.ServiceID = svc.ID
			ctr = tr
			xtr = append(xtr, tr)
		}
		return nil
	})
	if err != nil {
		return xtr, fmt.Errorf("unable to query: %w", err)
	}
	return xtr, nil
}

func (s StormStorage) GetTimedResponses(svc *models.Service, limit int, reverse bool) ([]*models.TimedResponse, error) {
	if svc.ID == "" {
		return nil, fmt.Errorf("service has no ID: %w", interactor.ErrNotFound)
	}
	xtr := []*models.TimedResponse{}
	err := s.db.Bolt.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(bucketName)).Bucket([]byte(svc.ID))
		if b == nil {
			return interactor.ErrNotFound
		}
		it := NewIterator(b.Cursor(), reverse)

		for k, v := it.initial(); k != nil && (it.count < limit || limit == 0); k, v = it.next() {
			t := &TimedResponse{}
			if err := proto.Unmarshal(v, t); err != nil {
				return fmt.Errorf("unable to unmarshal data: %w", err)
			}
			tr, err := newTimedResponseFromProto(k, t)
			if err != nil {
				return fmt.Errorf("unable to parse timed response: %w", err)
			}
			tr.ServiceID = svc.ID
			xtr = append(xtr, tr)
			it.add()
		}
		return nil
	})
	if err != nil {
		return xtr, fmt.Errorf("unable to query: %w", err)
	}
	return xtr, nil
}

func (s StormStorage) CountTimedResponses(svc *models.Service) (int, error) {
	if svc.ID == "" {
		return 0, interactor.ErrNotFound
	}
	var count int
	err := s.db.Bolt.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(bucketName)).Bucket([]byte(svc.ID))
		if b == nil {
			return interactor.ErrNotFound
		}
		c := b.Cursor()
		for k, _ := c.First(); k != nil; k, _ = c.Next() {
			count++
		}
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("count timed responses: %w", err)
	}
	return count, nil
}

func (s StormStorage) SaveTimedResponse(tr *models.TimedResponse) error {
	start := time.Now()
	if tr.ServiceID == "" {
		return errors.New("timed response has no service id associated")
	}
	id := tr.ID
	if id == xid.NilID() {
		id = xid.New()
	}
	pbtr := &TimedResponse{
		Status: int32(tr.Status),
		Server: durationpb.New(tr.Server),
		Total:  durationpb.New(tr.Total),
	}
	v, err := proto.Marshal(pbtr)
	if err != nil {
		return fmt.Errorf("marshal proto: %w", err)
	}
	err = s.db.Bolt.Update(func(tx *bolt.Tx) error {
		var b *bolt.Bucket
		var err error

		if b, err = tx.CreateBucketIfNotExists([]byte(bucketName)); err != nil {
			return fmt.Errorf("create or get bucket: %w", err)
		}
		if b, err = b.CreateBucketIfNotExists([]byte(tr.ServiceID)); err != nil {
			return fmt.Errorf("create or get bucket: %w", err)
		}
		if err = b.Put(id.Bytes(), v); err != nil {
			return fmt.Errorf("unable to put timed response '%s': %w", tr.ID.String(), err)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("transaction failed: %w", err)
	}

	// If the transaction succeeded, we can give the timed response its new id
	tr.ID = id
	s.log.Debug().Str("id", tr.ID.String()).Dur("took", time.Since(start)).Msg("saved timed reponse")
	return nil
}
