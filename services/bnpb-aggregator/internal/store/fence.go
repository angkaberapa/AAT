package store

// Fencing token (P2 M2).
//
// Lease Kubernetes menentukan SIAPA yang boleh polling, tetapi tidak bisa mencegah leader lama
// yang "bangun" (VM freeze, GC pause, jam bergeser) melanjutkan tulisannya sebelum sadar
// lease-nya sudah hilang. Karena itu setiap kali menjadi leader, instance mengambil epoch baru
// (AcquireEpoch, $inc atomik di storage), dan SETIAP tulisan hasil poll dilakukan di dalam
// transaksi yang juga menulis dokumen fence dengan syarat epoch == epoch miliknya. Begitu ada
// leader baru (epoch lebih besar), semua tulisan leader lama ditolak storage dengan ErrFenced.
// Storage-lah wasit terakhirnya; tidak ada perbandingan jam dinding antar node.

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/angkaberapa/AAT/services/bnpb-aggregator/internal/domain"
)

const (
	leadershipCollection = "leadership"
	sourceStatusCollect  = "source_status"
	fenceDocID           = "ingest"
)

// ErrFenced: tulisan ditolak karena epoch pemanggil sudah bukan epoch terbaru, artinya sudah
// ada leader yang lebih baru. Pemanggil wajib berhenti bertindak sebagai leader.
var ErrFenced = errors.New("write rejected: stale leader epoch")

// AcquireEpoch menaikkan epoch secara atomik dan mengembalikan nilai barunya (fencing token).
func (s *Store) AcquireEpoch(ctx context.Context, holder string) (int64, error) {
	var doc struct {
		Epoch int64 `bson:"epoch"`
	}
	err := s.leadership.FindOneAndUpdate(ctx,
		bson.D{{Key: "_id", Value: fenceDocID}},
		bson.D{
			{Key: "$inc", Value: bson.D{{Key: "epoch", Value: int64(1)}}},
			{Key: "$set", Value: bson.D{{Key: "holder", Value: holder}}},
			{Key: "$currentDate", Value: bson.D{{Key: "acquired_at", Value: true}}},
		},
		options.FindOneAndUpdate().SetUpsert(true).SetReturnDocument(options.After),
	).Decode(&doc)
	if err != nil {
		return 0, fmt.Errorf("acquire leader epoch: %w", err)
	}
	return doc.Epoch, nil
}

// withFence menjalankan fn dalam satu transaksi yang diawali penulisan dokumen fence bersyarat.
// Karena dokumen fence ditulis (bukan sekadar dibaca), transaksi leader lama dan AcquireEpoch
// leader baru bertabrakan sebagai write conflict; urutannya selalu linear.
func (s *Store) withFence(ctx context.Context, epoch int64, fn func(ctx context.Context) error) error {
	session, err := s.client.StartSession()
	if err != nil {
		return fmt.Errorf("start session: %w", err)
	}
	defer session.EndSession(ctx)

	_, err = session.WithTransaction(ctx, func(ctx context.Context) (any, error) {
		res, err := s.leadership.UpdateOne(ctx,
			bson.D{{Key: "_id", Value: fenceDocID}, {Key: "epoch", Value: epoch}},
			bson.D{{Key: "$currentDate", Value: bson.D{{Key: "last_write_at", Value: true}}}})
		if err != nil {
			return nil, err
		}
		if res.MatchedCount == 0 {
			return nil, ErrFenced
		}
		return nil, fn(ctx)
	})
	return err
}

// SaveBatch menyimpan hasil satu siklus poll secara atomik dan ter-fence: semua event tersimpan
// atau tidak sama sekali, dan hanya bila epoch masih yang terbaru.
func (s *Store) SaveBatch(ctx context.Context, epoch int64, events []domain.HazardEvent) (map[SaveResult]int, error) {
	var counts map[SaveResult]int
	err := s.withFence(ctx, epoch, func(ctx context.Context) error {
		// WithTransaction bisa mengulang fn; hitungan di-reset tiap percobaan.
		counts = map[SaveResult]int{}
		for _, e := range events {
			result, err := s.save(ctx, e)
			if err != nil {
				return err
			}
			counts[result]++
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return counts, nil
}

// SourceStatusRecord = keadaan mentah poll satu sumber, dibagikan ke semua instance lewat storage
// supaya follower juga bisa melaporkan stale_since dengan benar.
type SourceStatusRecord struct {
	Source       domain.Source `bson:"_id"`
	LastSuccess  time.Time     `bson:"last_success"`
	LastError    time.Time     `bson:"last_error"`
	LastErrorMsg string        `bson:"last_error_msg"`
	FailingSince time.Time     `bson:"failing_since"`
}

// SaveSourceStatus menyimpan status satu sumber, ter-fence seperti hasil poll.
func (s *Store) SaveSourceStatus(ctx context.Context, epoch int64, rec SourceStatusRecord) error {
	return s.withFence(ctx, epoch, func(ctx context.Context) error {
		_, err := s.statuses.ReplaceOne(ctx,
			bson.D{{Key: "_id", Value: rec.Source}}, rec, options.Replace().SetUpsert(true))
		return err
	})
}

// SourceStatuses membaca status semua sumber yang pernah dicatat.
func (s *Store) SourceStatuses(ctx context.Context) ([]SourceStatusRecord, error) {
	cur, err := s.statuses.Find(ctx, bson.D{})
	if err != nil {
		return nil, fmt.Errorf("find source status: %w", err)
	}
	var out []SourceStatusRecord
	if err := cur.All(ctx, &out); err != nil {
		return nil, fmt.Errorf("decode source status: %w", err)
	}
	return out, nil
}

// IsFenced melaporkan apakah err berasal dari penolakan fencing.
func IsFenced(err error) bool {
	return errors.Is(err, ErrFenced)
}
