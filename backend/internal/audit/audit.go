// Package audit writes the append-only, hash-chained audit log (spec 08).
//
// hash = SHA256(prev_hash || canonical JSON of the row). Values are hashed in
// the form PostgreSQL returns them (jsonb::text, inet::text), so Verify can
// recompute the chain from stored rows alone.
package audit

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const chainLockID = 727002

type Entry struct {
	Actor      string
	Action     string
	ObjectType string
	ObjectID   string
	Reason     string
	IP         string
	Result     string
	Data       any
}

type row struct {
	At         string `json:"at"`
	Actor      string `json:"actor"`
	Action     string `json:"action"`
	ObjectType string `json:"object_type"`
	ObjectID   string `json:"object_id"`
	Reason     string `json:"reason"`
	IP         string `json:"ip"`
	Result     string `json:"result"`
	Data       string `json:"data"`
}

func hashRow(prev []byte, r row) []byte {
	canon, _ := json.Marshal(r) // struct field order is fixed
	h := sha256.New()
	h.Write(prev)
	h.Write(canon)
	return h.Sum(nil)
}

// Write appends one entry inside tx. The advisory lock serialises writers so
// the chain never forks.
func Write(ctx context.Context, tx pgx.Tx, e Entry) error {
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, chainLockID); err != nil {
		return err
	}
	var prev []byte
	err := tx.QueryRow(ctx, `SELECT hash FROM audit_log ORDER BY id DESC LIMIT 1`).Scan(&prev)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	dataJSON, err := json.Marshal(e.Data)
	if err != nil {
		return err
	}
	var dataText, ipText string
	if err := tx.QueryRow(ctx, `SELECT $1::jsonb::text, COALESCE(NULLIF($2,'')::inet::text,'')`,
		string(dataJSON), e.IP).Scan(&dataText, &ipText); err != nil {
		return err
	}
	at := time.Now().UTC().Truncate(time.Microsecond)
	r := row{at.Format(time.RFC3339Nano), e.Actor, e.Action, e.ObjectType, e.ObjectID, e.Reason, ipText, e.Result, dataText}
	_, err = tx.Exec(ctx, `INSERT INTO audit_log(at, actor, action, object_type, object_id, reason, ip, result, data, prev_hash, hash)
		VALUES ($1,$2,$3,NULLIF($4,''),NULLIF($5,''),NULLIF($6,''),NULLIF($7,'')::inet,NULLIF($8,''),$9::jsonb,$10,$11)`,
		at, e.Actor, e.Action, e.ObjectType, e.ObjectID, e.Reason, ipText, e.Result, dataText, prev, hashRow(prev, r))
	return err
}

// VerifyResult reports the first broken link, if any.
type VerifyResult struct {
	Rows     int    `json:"rows"`
	OK       bool   `json:"ok"`
	Pruned   bool   `json:"pruned"` // older rows were removed by retention; the chain starts at the oldest row kept
	BrokenAt int64  `json:"broken_at,omitempty"`
	Problem  string `json:"problem,omitempty"`
}

// Verify walks the whole chain and recomputes every hash.
func Verify(ctx context.Context, pool *pgxpool.Pool) (VerifyResult, error) {
	rows, err := pool.Query(ctx, `SELECT id, at, actor, action, COALESCE(object_type,''), COALESCE(object_id,''),
		COALESCE(reason,''), COALESCE(ip::text,''), COALESCE(result,''), COALESCE(data::text,'null'), prev_hash, hash
		FROM audit_log ORDER BY id`)
	if err != nil {
		return VerifyResult{}, err
	}
	defer rows.Close()
	var res VerifyResult
	var last []byte
	for rows.Next() {
		var id int64
		var at time.Time
		var r row
		var prev, hash []byte
		if err := rows.Scan(&id, &at, &r.Actor, &r.Action, &r.ObjectType, &r.ObjectID, &r.Reason, &r.IP, &r.Result, &r.Data, &prev, &hash); err != nil {
			return res, err
		}
		res.Rows++
		r.At = at.UTC().Format(time.RFC3339Nano)
		if res.Rows == 1 && len(prev) > 0 {
			// retention removed the rows before this one: its prev_hash is the anchor
			res.Pruned, last = true, prev
		}
		if !bytes.Equal(prev, last) {
			res.BrokenAt, res.Problem = id, "prev_hash does not match previous row"
			return res, nil
		}
		if !bytes.Equal(hashRow(prev, r), hash) {
			res.BrokenAt, res.Problem = id, fmt.Sprintf("hash mismatch (action %s)", r.Action)
			return res, nil
		}
		last = hash
	}
	res.OK = true
	return res, rows.Err()
}
