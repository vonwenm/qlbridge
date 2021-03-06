package datasource

import (
	"fmt"
	"strings"
	"sync"

	u "github.com/araddon/gou"
	"github.com/araddon/qlbridge/expr"
)

var (
	_ = u.EMPTY

	// the data sources mutex
	sourceMu sync.Mutex
	// registry for data sources
	sources = newDataSources()

	// ensure our DataSourceFeatures is also DataSource
	_ DataSource = (*DataSourceFeatures)(nil)
)

/*

DataSource:   = Datasource config/defintion, will open connections
                  if Conn's are not session/specific you may
                  return DataSource itself

SourceConn:   = A connection to datasource, session/conn specific

*/

// We do type introspection in advance to speed up runtime
// feature detection for datasources
type Features struct {
	Scan         bool
	Seek         bool
	Where        bool
	GroupBy      bool
	Sort         bool
	Aggregations bool
}

// A datasource is most likely a database, file, api, in-mem data etc
// something that provides data rows.  If the source is a regular database
// it can do its own Filter, Seek, Sort, etc.   It may not implement
// all features of a database, in which case we will use our own
// execution engine.
//
// Minimum Features:
//  - Scanning:   iterate through messages/rows, use expr to evaluate
//                this is the minium we need to implement sql select
//  - Schema Tables:  at a minium tables available, the column level data
//                    can be introspected so is optional
//
// Optional Features:
//  - Seek          ie, key-value lookup, or indexed rows
//  - Projection    ie, selecting specific fields
//  - Where         filtering response
//  - GroupBy
//  - Aggregations  ie, count(*), avg()   etc
//  - Sort          sort response, very important for fast joins
//
// Non Select based Sql DML Operations:
//  - Delete
//  - Update
//  - Upsert
//  - Insert
//
// DDL/Schema Operations
//  - schema discovery
//  - create
//  - index
type DataSource interface {
	Tables() []string
	Open(connInfo string) (SourceConn, error)
	Close() error
}

// Connection, only one guaranteed feature, although
// should implement many more (scan, seek, etc)
type SourceConn interface {
	Close() error
}

// Some sources can do their own planning
type SourcePlanner interface {
	// Accept a sql statement, to plan the execution
	//  ideally, this would be done by planner but, we need
	//  source specific planners, as each backend has different features
	Accept(expr.SubVisitor) (Scanner, error)
}

type DataSourceFeatures struct {
	Features Features
	DataSource
}

// A scanner, most basic of data sources, just iterate through
//  rows without any optimizations
type Scanner interface {
	// create a new iterator for underlying datasource
	CreateIterator(filter expr.Node) Iterator
	MesgChan(filter expr.Node) <-chan Message
}

// simple iterator interface for paging through a datastore Messages/rows
// - used for scanning
// - for datasources that implement exec.Visitor() (ie, select) this
//    represents the alreader filtered, calculated rows
type Iterator interface {
	Next() Message
}

// Interface for Seeking row values instead of scanning (ie, Indexed)
type Seeker interface {
	DataSource
	// Just because we have Get, Multi-Get, doesn't mean we can seek all
	// expressions, find out.
	CanSeek(*expr.SqlSelect)
	Get(key string) Message
	MultiGet(keys []string) []Message
	// any seeker must also be a Scanner?
	//Scanner
}

type WhereFilter interface {
	DataSource
	Filter(expr.SqlStatement) error
}

type GroupBy interface {
	DataSource
	GroupBy(expr.SqlStatement) error
}

type Sort interface {
	DataSource
	Sort(expr.SqlStatement) error
}

type Aggregations interface {
	DataSource
	Aggregate(expr.SqlStatement) error
}

// Some data sources that implement more features, can provide
//  their own projection.
type Projection interface {
	// Describe the Columns etc
	Projection() (*expr.Projection, error)
}

// Our internal map of different types of datasources that are registered
// for our runtime system to use
type DataSources struct {
	sources      map[string]DataSource
	tableSources map[string]DataSource
}

func newDataSources() *DataSources {
	return &DataSources{
		sources:      make(map[string]DataSource),
		tableSources: make(map[string]DataSource),
	}
}

func NewFeaturedSource(src DataSource) *DataSourceFeatures {
	f := Features{}
	if _, ok := src.(Scanner); ok {
		f.Scan = true
	}
	if _, ok := src.(Seeker); ok {
		f.Seek = true
	}
	return &DataSourceFeatures{f, src}
}

func (m *DataSources) Get(sourceType string) *DataSourceFeatures {
	if source, ok := m.sources[strings.ToLower(sourceType)]; ok {
		u.Infof("found source: %v", sourceType)
		return NewFeaturedSource(source)
	}
	if len(m.sources) == 1 {
		for _, src := range m.sources {
			u.Warnf("only one source?")
			return NewFeaturedSource(src)
		}
	}
	if sourceType == "" {
		u.LogTracef(u.WARN, "No Source Type?")
	} else {
		u.Debugf("datasource.Get('%v')", sourceType)
	}

	if len(m.tableSources) == 0 {
		for _, src := range m.sources {
			tbls := src.Tables()
			for _, tbl := range tbls {
				if _, ok := m.tableSources[tbl]; ok {
					u.Warnf("table names must be unique across sources %v", tbl)
				} else {
					u.Debugf("creating tbl/source: %v  %T", tbl, src)
					m.tableSources[tbl] = src
				}
			}
		}
	}
	if src, ok := m.tableSources[sourceType]; ok {
		u.Debugf("found src with %v", sourceType)
		return NewFeaturedSource(src)
	} else {
		for src, _ := range m.sources {
			u.Debugf("source: %v", src)
		}
		u.LogTracef(u.WARN, "No table?  len(sources)=%d len(tables)=%v", len(m.sources), len(m.tableSources))
		u.Warnf("could not find table: %v  tables:%v", sourceType, m.tableSources)
	}
	return nil
}

func (m *DataSources) String() string {
	sourceNames := make([]string, 0, len(m.sources))
	for source, _ := range m.sources {
		sourceNames = append(sourceNames, source)
	}
	return fmt.Sprintf("{Sources: [%s] }", strings.Join(sourceNames, ", "))
}

// get registry of all datasource types
func DataSourcesRegistry() *DataSources {
	return sources
}

// Register makes a datasource available by the provided name.
// If Register is called twice with the same name or if source is nil,
// it panics.
func Register(name string, source DataSource) {
	if source == nil {
		panic("qlbridge/datasource: Register driver is nil")
	}
	name = strings.ToLower(name)
	u.Warnf("register datasource: %v %T", name, source)
	//u.LogTracef(u.WARN, "adding source %T to registry", source)
	sourceMu.Lock()
	defer sourceMu.Unlock()
	if _, dup := sources.sources[name]; dup {
		panic("qlbridge/datasource: Register called twice for datasource " + name)
	}
	sources.sources[name] = source
}

// Open a datasource
//  sourcename = "csv", "elasticsearch"
func OpenConn(sourceName, sourceConfig string) (SourceConn, error) {
	sourcei, ok := sources.sources[sourceName]
	if !ok {
		return nil, fmt.Errorf("datasource: unknown source %q (forgotten import?)", sourceName)
	}
	source, err := sourcei.Open(sourceConfig)
	if err != nil {
		return nil, err
	}
	return source, nil
}

func SourceIterChannel(iter Iterator, filter expr.Node, sigCh <-chan bool) <-chan Message {

	out := make(chan Message, 100)

	go func() {
		defer func() {
			if r := recover(); r != nil {
				u.Errorf("recover panic: %v", r)
			}
			// Can we safely close this?
			close(out)
		}()
		for item := iter.Next(); item != nil; item = iter.Next() {

			//u.Infof("In source Scanner iter %#v", item)
			select {
			case <-sigCh:
				u.Warnf("got signal quit")

				return
			case out <- item:
				// continue
			}
		}
	}()
	return out
}
