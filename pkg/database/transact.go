package database

import (
	"log"
	"time"
)

// BEGIN TRANSACTION; f ... ; COMMIT; or
// BEGIN TRANSACTION; f ... ; ROLLBACK; if f returns an error.
func (h *sqlHelper) Transact(db QueryableDB, f func(tx Tx) error) error {
	var err error

	for attempts := 0; attempts < 3; attempts++ {
		err = func() error {
			tx, err := db.Begin()
			if err != nil {
				return err
			}
			defer tx.Rollback()

			err = f(tx)
			if err != nil {
				return err
			}

			err = tx.Commit()
			if err != nil {
				log.Printf("failed-committing-transaction %s", err.Error())

			}
			return err
		}()

		// golang sql package does not always retry query on ErrBadConn, e.g. if it
		// is in the middle of a transaction. This make sense since the package
		// cannot retry the entire transaction and has to return control to the
		// caller to initiate a retry
		if attempts >= 2 {
			break
		} else {
			log.Printf("deadlock-transaction", err.Error())
			time.Sleep(500 * time.Millisecond)
		}
	}

	return err
}
