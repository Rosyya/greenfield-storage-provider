package store

import (
	"fmt"
	"os"

	"github.com/bnb-chain/greenfield-storage-provider/model"
	"github.com/bnb-chain/greenfield-storage-provider/store/config"
	"github.com/bnb-chain/greenfield-storage-provider/store/jobdb/jobmemory"
	"github.com/bnb-chain/greenfield-storage-provider/store/jobdb/jobsql"
	"github.com/bnb-chain/greenfield-storage-provider/store/metadb/metalevel"
	"github.com/bnb-chain/greenfield-storage-provider/store/metadb/metasql"
	"github.com/bnb-chain/greenfield-storage-provider/store/spdb"
)

// NewMetaDB return a meta-db instance
func NewMetaDB(dbType string, levelDBConfig *config.LevelDBConfig, sqlDBConfig *config.SqlDBConfig) (spdb.MetaDB, error) {
	var (
		metaDB spdb.MetaDB
		err    error
	)

	switch dbType {
	case model.MySqlDB:
		// load meta db config from env vars
		sqlDBConfig.User = os.Getenv(model.MetaDBUser)
		sqlDBConfig.Passwd = os.Getenv(model.MetaDBPassword)
		metaDB, err = metasql.NewMetaDB(sqlDBConfig)
	case model.LevelDB:
		metaDB, err = metalevel.NewMetaDB(levelDBConfig)
	default:
		err = fmt.Errorf("meta db not support %s type", dbType)
	}
	return metaDB, err
}

// NewJobDB return a job-db instance
func NewJobDB(dbType string, sqlDBConfig *config.SqlDBConfig) (spdb.JobDB, error) {
	var (
		jobDB spdb.JobDB
		err   error
	)

	switch dbType {
	case model.MySqlDB:
		// load job db config from env vars
		sqlDBConfig.User = os.Getenv(model.JobDBUser)
		sqlDBConfig.Passwd = os.Getenv(model.JobDBPassword)
		jobDB, err = jobsql.NewJobMetaImpl(sqlDBConfig)
	case model.MemoryDB:
		jobDB = jobmemory.NewMemJobDB()
	default:
		err = fmt.Errorf("job db not support %s type", dbType)
	}
	return jobDB, err
}