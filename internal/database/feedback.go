package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// ========== FUNZIONI FEEDBACK ==========

// SaveFeedback salva un nuovo feedback e aggiorna users.last_feedback_at
// nella stessa transazione (save_feedback).
//
// feedbackType: 'command', 'notification_rating', 'periodic_survey';
// rating: 1-5 per survey, 1/0 per thumbs, nil se non applicabile;
// comment: commento opzionale. L'utente deve esistere (foreign key).
func (s *Store) SaveFeedback(userID, feedbackType string, rating *int, comment *string) bool {
	var ratingParam, commentParam any
	if rating != nil {
		ratingParam = int64(*rating)
	}
	if comment != nil {
		commentParam = *comment
	}

	ctx := context.Background()
	err := func() error {
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
                INSERT INTO feedback (user_id, feedback_type, rating, comment)
                VALUES (?, ?, ?, ?)
            `, userID, feedbackType, ratingParam, commentParam); err != nil {
			_ = tx.Rollback()
			return err
		}
		// Aggiorna timestamp ultimo feedback
		if _, err := tx.ExecContext(ctx,
			"UPDATE users SET last_feedback_at = CURRENT_TIMESTAMP WHERE user_id = ?", userID); err != nil {
			_ = tx.Rollback()
			return err
		}
		return tx.Commit()
	}()
	if err != nil {
		logDBError(err)
		logger.Error(fmt.Sprintf("❌ Errore salvataggio feedback: %v", err))
		return false
	}
	logger.Info(fmt.Sprintf("💬 Feedback salvato per utente %s (tipo: %s)", userID, feedbackType))
	return true
}

// GetLastFeedbackTime restituisce il timestamp dell'ultimo feedback
// dell'utente così come salvato da SQLite CURRENT_TIMESTAMP
// ("YYYY-MM-DD HH:MM:SS", UTC), oppure nil se l'utente non ha mai dato
// feedback, non esiste o in caso di errore (get_last_feedback_time).
func (s *Store) GetLastFeedbackTime(userID string) *string {
	var v sql.NullString
	// CAST evita la conversione automatica a time.Time del driver per le
	// colonne TIMESTAMP: Python restituisce la stringa grezza.
	err := s.db.QueryRowContext(context.Background(),
		"SELECT CAST(last_feedback_at AS TEXT) AS last_feedback_at FROM users WHERE user_id = ?",
		userID).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		logDBError(err)
		logger.Error(fmt.Sprintf("❌ Errore recupero last_feedback_time: %v", err))
		return nil
	}
	return nullStringPtr(v)
}

// GetRecentFeedbacks restituisce gli ultimi limit feedback (per admin),
// ordinati per created_at (ora locale) decrescente e id decrescente
// (get_recent_feedbacks; il default Python è limit=10). Su errore
// restituisce una slice vuota.
func (s *Store) GetRecentFeedbacks(limit int) []Feedback {
	feedbacks := []Feedback{}
	rows, err := s.db.QueryContext(context.Background(), `
                SELECT
                    id, user_id, feedback_type, rating, comment,
                    datetime(created_at, 'localtime') as created_at
                FROM feedback
                ORDER BY created_at DESC, id DESC
                LIMIT ?
            `, limit)
	if err == nil {
		err = func() error {
			defer rows.Close()
			for rows.Next() {
				var f Feedback
				var rating sql.NullInt64
				var comment, createdAt sql.NullString
				if err := rows.Scan(&f.ID, &f.UserID, &f.FeedbackType, &rating, &comment, &createdAt); err != nil {
					return err
				}
				if rating.Valid {
					r := int(rating.Int64)
					f.Rating = &r
				}
				f.Comment = nullStringPtr(comment)
				f.CreatedAt = nullStringPtr(createdAt)
				feedbacks = append(feedbacks, f)
			}
			return rows.Err()
		}()
	}
	if err != nil {
		logDBError(err)
		logger.Error(fmt.Sprintf("❌ Errore recupero feedback recenti: %v", err))
		return []Feedback{}
	}
	return feedbacks
}

// GetFeedbackCount restituisce il numero totale di feedback (get_feedback_count), 0 su errore.
func (s *Store) GetFeedbackCount() int {
	var n int
	err := s.db.QueryRowContext(context.Background(), "SELECT COUNT(*) as count FROM feedback").Scan(&n)
	if err != nil {
		logDBError(err)
		logger.Error(fmt.Sprintf("❌ Errore conteggio feedback: %v", err))
		return 0
	}
	return n
}
