package exec

import (
	"fmt"

	u "github.com/araddon/gou"
	"github.com/araddon/qlbridge/datasource"
	"github.com/araddon/qlbridge/expr"
)

var (
	_ = u.EMPTY

	// Ensure that we implement the sql expr.Visitor interface
	_ expr.Visitor    = (*JobBuilder)(nil)
	_ expr.SubVisitor = (*JobBuilder)(nil)
)

// This is a simple, single source Job Executor
//   we can create smarter ones but this is a basic implementation
type JobBuilder struct {
	schema   *datasource.RuntimeConfig
	connInfo string
	where    expr.Node
	distinct bool
	children Tasks
}

// JobBuilder
//   @connInfo = connection string info for original connection
//
func NewJobBuilder(rtConf *datasource.RuntimeConfig, connInfo string) *JobBuilder {
	b := JobBuilder{}
	b.schema = rtConf
	b.connInfo = connInfo
	return &b
}

func (m *JobBuilder) VisitSelect(stmt *expr.SqlSelect) (interface{}, error) {
	u.Debugf("VisitSelect %+v", stmt)

	tasks := make(Tasks, 0)

	if len(stmt.From) == 1 {
		// One From Source   This entire Source needs to be moved into
		//  a From().Accept(m) or m.visitSubselect()
		from := stmt.From[0]
		if from.Name != "" && from.Source == nil {
			u.Infof("get SourceConn: %v", from.Name)
			sourceConn := m.schema.Conn(from.Name)
			u.Debugf("sourceConn: %T  %#v", sourceConn, sourceConn)
			// Must provider either Scanner, and or Seeker interfaces
			if scanner, ok := sourceConn.(datasource.Scanner); !ok {
				return nil, fmt.Errorf("Must Implement Scanner")
			} else {
				in := NewSource(from, scanner)
				tasks.Add(in)
			}
		}
	} else {
		// for now, only support 1 join
		if len(stmt.From) != 2 {
			return nil, fmt.Errorf("3 or more Table/Join not currently implemented")
		}
		// We really need to move this Rewrite into Planner or a Finalizer()?
		// for _, from := range stmt.From {
		// 	from.Rewrite(stmt)
		// }
		// Fold 0 <- 1
		stmt.From[0].Rewrite(true, stmt)
		stmt.From[1].Rewrite(false, stmt)
		in, err := NewSourceJoin(m, stmt.From[0], stmt.From[1], m.schema)
		if err != nil {
			return nil, err
		}
		tasks.Add(in)
	}

	//u.Debugf("has where? %v", stmt.Where != nil)
	if stmt.Where != nil {
		switch {
		case stmt.Where.Source != nil:
			u.Warnf("Found un-supported subquery: %#v", stmt.Where)
		case stmt.Where.Expr != nil:
			where := NewWhere(stmt.Where.Expr)
			tasks.Add(where)
		default:
			u.Warnf("Found un-supported where type: %#v", stmt.Where)
		}

	}

	// Add a Projection
	projection := NewProjection(stmt)
	u.Infof("adding projection: %#v", projection)
	tasks.Add(projection)

	return tasks, nil
}

func (m *JobBuilder) VisitSubselect(stmt *expr.SqlSource) (interface{}, error) {
	u.Debugf("VisitSubselect %+v", stmt)
	return nil, expr.ErrNotImplemented
}

func (m *JobBuilder) VisitJoin(stmt *expr.SqlSource) (interface{}, error) {
	u.Debugf("VisitJoin %+v", stmt)
	return nil, expr.ErrNotImplemented
}

func (m *JobBuilder) VisitInsert(stmt *expr.SqlInsert) (interface{}, error) {
	u.Debugf("VisitInsert %+v", stmt)
	return nil, expr.ErrNotImplemented
}

func (m *JobBuilder) VisitDelete(stmt *expr.SqlDelete) (interface{}, error) {
	u.Debugf("VisitDelete %+v", stmt)
	return nil, expr.ErrNotImplemented
}

func (m *JobBuilder) VisitUpdate(stmt *expr.SqlUpdate) (interface{}, error) {
	u.Debugf("VisitUpdate %+v", stmt)
	return nil, expr.ErrNotImplemented
}

func (m *JobBuilder) VisitUpsert(stmt *expr.SqlUpsert) (interface{}, error) {
	u.Debugf("VisitUpdate %+v", stmt)
	return nil, expr.ErrNotImplemented
}

func (m *JobBuilder) VisitShow(stmt *expr.SqlShow) (interface{}, error) {
	u.Debugf("VisitShow %+v", stmt)
	return nil, expr.ErrNotImplemented
}

func (m *JobBuilder) VisitDescribe(stmt *expr.SqlDescribe) (interface{}, error) {
	u.Debugf("VisitDescribe %+v", stmt)
	return nil, expr.ErrNotImplemented
}

func (m *JobBuilder) VisitPreparedStmt(stmt *expr.PreparedStatement) (interface{}, error) {
	u.Debugf("VisitPreparedStmt %+v", stmt)
	return nil, expr.ErrNotImplemented
}
