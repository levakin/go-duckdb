// Use of this source code is governed by a MIT-style
// license that can be found in the LICENSE file.

// Package duckdb implements a database/sql driver for the DuckDB database.
package duckdb

/*
#include <duckdb.h>
*/
import "C"

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"unsafe"
)

func init() {
	sql.Register("duckdb", Driver{})
}

type Driver struct{}

func (d Driver) Open(dataSourceName string) (driver.Conn, error) {
	connector, err := d.OpenConnector(dataSourceName)
	if err != nil {
		return nil, err
	}
	return connector.Connect(context.Background())
}

func (Driver) OpenConnector(dsn string) (driver.Connector, error) {
	return NewConnector(dsn, func(execerContext driver.ExecerContext) error { return nil })
}

// NewConnector opens a new Connector for the DuckDB database.
// It's user's responsibility to close the returned Connector in case it's not passed to the sql.OpenDB function.
// sql.DB will close the Connector when sql.DB.Close() is called.
func NewConnector(dsn string, connInitFn func(execer driver.ExecerContext) error) (*Connector, error) {
	var db C.duckdb_database

	parsedDSN, err := url.Parse(dsn)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", errParseConfig, err.Error())
	}

	config, err := prepareConfig(parsedDSN)
	if err != nil {
		return nil, err
	}

	connectionString := C.CString(extractConnectionString(dsn))
	defer C.free(unsafe.Pointer(connectionString))

	var errMsg *C.char
	defer C.duckdb_free(unsafe.Pointer(errMsg))

	if state := C.duckdb_open_ext(connectionString, &db, config, &errMsg); state == C.DuckDBError {
		C.duckdb_destroy_config(&config)

		return nil, fmt.Errorf("%w: %s", errOpen, C.GoString(errMsg))
	}

	return &Connector{
		db:         &db,
		connInitFn: connInitFn,
		config:     config,
	}, nil
}

type Connector struct {
	db         *C.duckdb_database
	config     C.duckdb_config
	connInitFn func(execer driver.ExecerContext) error
}

func (c *Connector) Driver() driver.Driver {
	return Driver{}
}

func (c *Connector) Connect(context.Context) (driver.Conn, error) {
	var con C.duckdb_connection
	if state := C.duckdb_connect(*c.db, &con); state == C.DuckDBError {
		return nil, errOpen
	}

	conn := &conn{con: &con}

	// Call the connection init function if defined
	if c.connInitFn != nil {
		if err := c.connInitFn(conn); err != nil {
			return nil, err
		}
	}

	return conn, nil
}

func (c *Connector) Close() error {
	C.duckdb_close(c.db)
	c.db = nil

	C.duckdb_destroy_config(&c.config)
	c.config = nil

	return nil
}

func extractConnectionString(dataSourceName string) string {
	var queryIndex = strings.Index(dataSourceName, "?")
	if queryIndex < 0 {
		queryIndex = len(dataSourceName)
	}

	return dataSourceName[0:queryIndex]
}

func prepareConfig(parsedDSN *url.URL) (C.duckdb_config, error) {
	var config C.duckdb_config
	if state := C.duckdb_create_config(&config); state == C.DuckDBError {
		return nil, errCreateConfig
	}
	if state := C.duckdb_set_config(config, C.CString("duckdb_api"), C.CString("go")); state == C.DuckDBError {
		return nil, fmt.Errorf("%w: failed to set duckdb_api", errSetConfig)
	}

	if len(parsedDSN.RawQuery) > 0 {
		for k, v := range parsedDSN.Query() {
			if len(v) > 0 {
				if err := setConfig(config, k, v[0]); err != nil {
					C.duckdb_destroy_config(&config)

					return nil, err
				}
			}
		}
	}

	return config, nil
}

func setConfig(config C.duckdb_config, name, option string) error {
	if state := C.duckdb_set_config(config, C.CString(name), C.CString(option)); state == C.DuckDBError {
		return fmt.Errorf("%w: affected config option %s=%s", errSetConfig, name, option)
	}

	return nil
}

var (
	errOpen         = errors.New("could not open database")
	errParseConfig  = errors.New("could not parse config for database")
	errCreateConfig = errors.New("could not create config for database")
	errSetConfig    = errors.New("could not set config for database")
)
