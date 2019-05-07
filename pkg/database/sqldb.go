package database

type SQLDB struct {
	db     QueryableDB
	flavor string
	helper SQLHelper
}

func NewSQLDB(
	db QueryableDB,
	flavor string,
) *SQLDB {
	helper := NewSQLHelper(flavor)
	return &SQLDB{
		db:     db,
		flavor: flavor,
		helper: helper,
	}
}
