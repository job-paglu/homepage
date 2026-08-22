package jobs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Row scanning and driver-error translation. Split out of repository.go so that
// file contains SQL and nothing else.

// scanner is satisfied by both pgx.Row and pgx.Rows, so one scan helper serves
// QueryRow and an iterated Query.
type scanner interface {
	Scan(dest ...any) error
}

// scanTargets returns the destination pointers for jobColumns, in that exact
// order.
//
// The three out-parameters exist because Postgres hands back text for
// job_type, status and remote_type, and pgx will not scan text into a named
// string type. They are converted by the callers below.
//
// Written once and shared by both scan functions: the two column lists have to
// stay identical to jobColumns, and a single list is one place to get that
// wrong instead of two.
func scanTargets(r *Record, jobType, status *string, remote **string) []any {
	return []any{
		&r.ID, &r.Slug, &r.Title, &r.Description, &r.Salary, &r.Qualification, jobType, &r.Location,
		&r.ApplyURL, &r.Company, &r.Country, remote, &r.ExperienceLevel, &r.Category,
		&r.IsFeatured, status, &r.PublishedAt, &r.ExpiresAt, &r.CreatedAt, &r.UpdatedAt, &r.DeletedAt,
	}
}

func applyEnums(r *Record, jobType, status string, remote *string) {
	r.JobType = JobType(jobType)
	r.Status = JobStatus(status)
	if remote != nil {
		value := RemoteType(*remote)
		r.RemoteType = &value
	}
}

// scanRecord reads one row of jobColumns.
func scanRecord(s scanner) (Record, error) {
	var (
		record  Record
		jobType string
		status  string
		remote  *string
	)
	if err := s.Scan(scanTargets(&record, &jobType, &status, &remote)...); err != nil {
		return Record{}, fmt.Errorf("scanning job row: %w", err)
	}
	applyEnums(&record, jobType, status, remote)
	return record, nil
}

// scanRecordWithTotal reads one row of jobColumns plus the trailing
// count(*) OVER () that the list queries append.
func scanRecordWithTotal(s scanner) (Record, int64, error) {
	var (
		record  Record
		jobType string
		status  string
		remote  *string
		total   int64
	)
	targets := append(scanTargets(&record, &jobType, &status, &remote), &total)
	if err := s.Scan(targets...); err != nil {
		return Record{}, 0, fmt.Errorf("scanning job row: %w", err)
	}
	applyEnums(&record, jobType, status, remote)
	return record, total, nil
}

// remotePtr converts the named type back to a plain string for a bind parameter.
func remotePtr(r *RemoteType) *string {
	if r == nil {
		return nil
	}
	value := string(*r)
	return &value
}

// classify maps a driver error to one of this package's sentinels.
//
// The distinction that earns its keep is "unreachable" versus "wrong": the first
// is a 503 the frontend offers a retry for, the second is a 500 that needs a
// developer. Everything else is returned unchanged so the log keeps the detail
// and the service turns it into a content-free 500.
func classify(err error) error {
	if err == nil {
		return nil
	}

	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}

	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch {
		// A unique violation, matched by constraint name: jobs has more than one
		// unique index and they are not the same failure.
		case pgErr.Code == "23505" && strings.Contains(pgErr.ConstraintName, "slug"):
			return ErrSlugConflict
		case strings.HasPrefix(pgErr.Code, "08"), // connection exception
			pgErr.Code == "57P01", // admin shutdown
			pgErr.Code == "57P02", // crash shutdown
			pgErr.Code == "57P03", // cannot connect now — Neon resuming
			pgErr.Code == "53300", // too many connections
			pgErr.Code == "55P03": // lock not available
			return fmt.Errorf("%w: %s", ErrUnavailable, pgErr.Code)
		}
		return err
	}

	// Timeout, cancellation, refused connection, DNS failure, half-closed
	// socket: all "the database did not answer", none of them a bug in a query.
	var connectErr *pgconn.ConnectError
	var netErr net.Error
	switch {
	case errors.Is(err, context.DeadlineExceeded),
		errors.Is(err, context.Canceled),
		errors.Is(err, io.EOF),
		errors.Is(err, io.ErrUnexpectedEOF),
		errors.As(err, &connectErr),
		errors.As(err, &netErr):
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}

	return err
}
