package expr

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"time"

	u "github.com/araddon/gou"
	"github.com/araddon/qlbridge/lex"
	"github.com/araddon/qlbridge/value"
)

var (
	_ = u.EMPTY

	// our DataTypes we support, a limited sub-set of go
	floatRv   = reflect.ValueOf(float64(1.2))
	int64Rv   = reflect.ValueOf(int64(1))
	int32Rv   = reflect.ValueOf(int32(1))
	stringRv  = reflect.ValueOf("hello")
	stringsRv = reflect.ValueOf([]string{"hello"})
	boolRv    = reflect.ValueOf(true)
	mapIntRv  = reflect.ValueOf(map[string]int64{"hello": int64(1)})
	timeRv    = reflect.ValueOf(time.Time{})
	nilRv     = reflect.ValueOf(nil)

	// Standard errors
	ErrNotSupported   = fmt.Errorf("QLB: Not supported")
	ErrNotImplemented = fmt.Errorf("QLB: Not implemented")
	ErrUnknownCommand = fmt.Errorf("QLB: Unknown Command")
	ErrInternalError  = fmt.Errorf("QLB: Internal Error")
)

type NodeType uint8

const (
	NodeNodeType        NodeType = 1
	FuncNodeType        NodeType = 2
	IdentityNodeType    NodeType = 3
	StringNodeType      NodeType = 4
	NumberNodeType      NodeType = 5
	BinaryNodeType      NodeType = 10
	UnaryNodeType       NodeType = 11
	TriNodeType         NodeType = 13
	MultiArgNodeType    NodeType = 14
	NullNodeType        NodeType = 15
	SqlPreparedType     NodeType = 29
	SqlSelectNodeType   NodeType = 30
	SqlInsertNodeType   NodeType = 31
	SqlUpdateNodeType   NodeType = 32
	SqlUpsertNodeType   NodeType = 33
	SqlDeleteNodeType   NodeType = 35
	SqlDescribeNodeType NodeType = 40
	SqlShowNodeType     NodeType = 41
	SqlCreateNodeType   NodeType = 50
	SqlSourceNodeType   NodeType = 55
	SqlWhereNodeType    NodeType = 56
	SqlIntoNodeType     NodeType = 57
	SqlJoinNodeType     NodeType = 58
	//SetNodeType         NodeType = 12
)

// A Node is an element in the expression tree, implemented
// by different types (string, binary, urnary, func, case, etc)
//
type Node interface {
	// string representation of internals
	String() string

	// string representation matches original statement
	StringAST() string

	// byte position of start of node in full original input string
	Position() Pos

	// performs type checking for itself and sub-nodes, evaluates
	// validity of the expression/node in advance of evaluation
	Check() error

	// describes the Node type, faster than interface casting
	NodeType() NodeType
}

type ParsedNode interface {
	Finalize() error
}

// Node that has a Type Value
type NodeValueType interface {
	// describes the return type
	Type() reflect.Value
}

// Eval context, used to contain info for usage/lookup at runtime evaluation
type EvalContext interface {
	ContextReader
}

// Context Reader is interface to read the context of message/row/command
//  being evaluated
type ContextReader interface {
	Get(key string) (value.Value, bool)
	Row() map[string]value.Value
	Ts() time.Time
}

// For evaluation storage
type ContextWriter interface {
	Put(col SchemaInfo, readCtx ContextReader, v value.Value) error
	Delete(row map[string]value.Value) error
}

// for commiting row ops (insert, update)
type RowWriter interface {
	Commit(rowInfo []SchemaInfo, row RowWriter) error
	Put(col SchemaInfo, readCtx ContextReader, v value.Value) error
}

// Describes a function which wraps and allows native go functions
//  to be called (via reflection) via scripting
//
type Func struct {
	Name string
	// The arguments we expect
	Args            []reflect.Value
	VariadicArgs    bool
	Return          reflect.Value
	ReturnValueType value.ValueType
	// The actual Go Function
	F reflect.Value
}

// FuncNode holds a Func, which desribes a go Function as
// well as fulfilling the Pos, String() etc for a Node
//
// interfaces:   Node
type FuncNode struct {
	Pos
	Name string // Name of func
	F    Func   // The actual function that this AST maps to
	Args []Node // Arguments are them-selves nodes
}

// IdentityNode will look up a value out of a env bag
//  also identities of sql objects (tables, columns, etc)
//  we often need to rewrite these as in sql it is `table.column`
type IdentityNode struct {
	Pos
	Quote byte
	Text  string
	left  string
	right string
}

// StringNode holds a value literal, quotes not included
type StringNode struct {
	Pos
	Text string
}

type NullNode struct {
	Pos
}

// NumberNode holds a number: signed or unsigned integer or float.
// The value is parsed and stored under all the types that can represent the value.
// This simulates in a small amount of code the behavior of Go's ideal constants.
type NumberNode struct {
	Pos
	IsInt   bool    // Number has an integer value.
	IsFloat bool    // Number has a floating-point value.
	Int64   int64   // The integer value.
	Float64 float64 // The floating-point value.
	Text    string  // The original textual representation from the input.
}

// Binary node is   x op y, two nodes (left, right) and an operator
// operators can be a variety of:
//    +, -, *, %, /,
// Also, parenthesis may wrap these
type BinaryNode struct {
	Pos
	Paren    bool
	Args     [2]Node
	Operator lex.Token
}

// Tri Node
//    ARG1 Between ARG2 AND ARG3
type TriNode struct {
	Pos
	Args     [3]Node
	Operator lex.Token
}

// UnaryNode holds one argument and an operator
//    !eq(5,6)
//    !true
//    !(true OR false)
//    !toint(now())
type UnaryNode struct {
	Pos
	Arg      Node
	Operator lex.Token
}

// Set holds n nodes and has a variety of compares
//
// type SetNode struct {
// 	Pos
// 	Args     []Node
// 	Operator lex.Token
// }

// Multi Arg Node
//    arg0 IN (arg1,arg2.....)
//    5 in (1,2,3,4)   => false
type MultiArgNode struct {
	Pos
	Args     []Node
	Operator lex.Token
}

// Pos represents a byte position in the original input text which was parsed
type Pos int

func (p Pos) Position() Pos { return p }

// Recursively descend down a node looking for first Identity Field
//
//     min(year)                 == year
//     eq(min(item), max(month)) == item
func FindIdentityField(node Node) string {

	switch n := node.(type) {
	case *IdentityNode:
		return n.Text
	case *BinaryNode:
		for _, arg := range n.Args {
			return FindIdentityField(arg)
		}
	case *FuncNode:
		for _, arg := range n.Args {
			return FindIdentityField(arg)
		}
	}
	return ""
}

// Recursively descend down a node looking for first Identity Field
//   and combine with outermost expression to create an alias
//
//     min(year)                 == min_year
//     eq(min(year), max(month)) == eq_year
func FindIdentityName(depth int, node Node, prefix string) string {

	switch n := node.(type) {
	case *IdentityNode:
		if prefix == "" {
			return n.Text
		}
		return fmt.Sprintf("%s_%s", prefix, n.Text)
	case *BinaryNode:
		for _, arg := range n.Args {
			return FindIdentityName(depth+1, arg, strings.ToLower(arg.String()))
		}
	case *FuncNode:
		if depth > 10 {
			return ""
		}
		for _, arg := range n.Args {
			return FindIdentityName(depth+1, arg, strings.ToLower(n.F.Name))
		}
	}
	return ""

}

// Infer Value type from Node
func ValueTypeFromNode(n Node) value.ValueType {
	switch nt := n.(type) {
	case *FuncNode:
	case *StringNode:
		return value.StringType
	case *IdentityNode:
		// ??
		return value.StringType
	case *NumberNode:
		return value.NumberType
	case *BinaryNode:
		switch nt.Operator.T {
		case lex.TokenLogicAnd, lex.TokenLogicOr:
			return value.BoolType
		case lex.TokenMultiply, lex.TokenMinus, lex.TokenAdd, lex.TokenDivide:
			return value.NumberType
		case lex.TokenModulus:
			return value.IntType
		default:
			u.Warnf("NoValueType? %T", n)
		}
	case nil:
		return value.UnknownType
	default:
		u.Warnf("NoValueType? %T", n)
	}
	return value.UnknownType
}

func NewFuncNode(pos Pos, name string, f Func) *FuncNode {
	return &FuncNode{Pos: pos, Name: name, F: f}
}

func (c *FuncNode) append(arg Node) {
	c.Args = append(c.Args, arg)
}

func (c *FuncNode) String() string {
	s := c.Name + "("
	for i, arg := range c.Args {
		if i > 0 {
			s += ", "
		}
		s += arg.String()
	}
	s += ")"
	return s
}

func (c *FuncNode) StringAST() string {
	s := c.Name + "("
	for i, arg := range c.Args {
		//u.Debugf("arg: %v   %T %v", arg, arg, arg.StringAST())
		if i > 0 {
			s += ", "
		}
		s += arg.StringAST()
	}
	s += ")"
	return s
}

func (c *FuncNode) Check() error {

	if len(c.Args) < len(c.F.Args) && !c.F.VariadicArgs {
		return fmt.Errorf("parse: not enough arguments for %s  supplied:%d  f.Args:%v", c.Name, len(c.Args), len(c.F.Args))
	} else if (len(c.Args) >= len(c.F.Args)) && c.F.VariadicArgs {
		// ok
	} else if len(c.Args) > len(c.F.Args) {
		u.Warnf("lenc.Args >= len(c.F.Args?  %v", (len(c.Args) >= len(c.F.Args)))
		err := fmt.Errorf("parse: too many arguments for %s want:%v got:%v   %#v", c.Name, len(c.F.Args), len(c.Args), c.Args)
		u.Errorf("funcNode.Check(): %v", err)
		return err
	}
	for i, a := range c.Args {
		switch a.(type) {
		case Node:
			if err := a.Check(); err != nil {
				return err
			}
		case value.Value:
			// TODO: we need to check co-ercion here, ie which Args can be converted to what types
			if nodeVal, ok := a.(NodeValueType); ok {
				// For Env Variables, we need to Check those (On Definition?)
				if c.F.Args[i].Kind() != nodeVal.Type().Kind() {
					u.Errorf("error in parse Check(): %v", a)
					return fmt.Errorf("parse: expected %v, got %v    ", nodeVal.Type().Kind(), c.F.Args[i].Kind())
				}
				if err := a.Check(); err != nil {
					return err
				}
			}

		}

	}
	return nil
}

func (f *FuncNode) NodeType() NodeType  { return FuncNodeType }
func (f *FuncNode) Type() reflect.Value { return f.F.Return }

// NewNumber is a little weird in that this Node accepts string @text
// and uses go to parse into Int, AND Float.
func NewNumber(pos Pos, text string) (*NumberNode, error) {
	n := &NumberNode{Pos: pos, Text: text}
	// Do integer test first so we get 0x123 etc.
	u, err := strconv.ParseInt(text, 0, 64) // will fail for -0.
	if err == nil {
		n.IsInt = true
		n.Int64 = u
	}
	// If an integer extraction succeeded, promote the float.
	if n.IsInt {
		n.IsFloat = true
		n.Float64 = float64(n.Int64)
	} else {
		f, err := strconv.ParseFloat(text, 64)
		if err == nil {
			n.IsFloat = true
			n.Float64 = f
			// If a floating-point extraction succeeded, extract the int if needed.
			if !n.IsInt && float64(int64(f)) == f {
				n.IsInt = true
				n.Int64 = int64(f)
			}
		}
	}
	if !n.IsInt && !n.IsFloat {
		return nil, fmt.Errorf("illegal number syntax: %q", text)
	}
	return n, nil
}

func (n *NumberNode) String() string {
	return n.Text
}

func (n *NumberNode) StringAST() string {
	return n.String()
}

func (n *NumberNode) Check() error {
	return nil
}

func (m *NumberNode) NodeType() NodeType  { return NumberNodeType }
func (n *NumberNode) Type() reflect.Value { return floatRv }

func NewStringNode(pos Pos, text string) *StringNode {
	return &StringNode{Pos: pos, Text: text}
}
func (m *StringNode) String() string      { return m.Text }
func (m *StringNode) StringAST() string   { return fmt.Sprintf("%q", m.Text) }
func (m *StringNode) Check() error        { return nil }
func (m *StringNode) NodeType() NodeType  { return StringNodeType }
func (m *StringNode) Type() reflect.Value { return stringRv }

func NewIdentityNode(tok *lex.Token) *IdentityNode {
	return &IdentityNode{Pos: Pos(tok.Pos), Text: tok.V, Quote: tok.Quote}
}

func (m *IdentityNode) String() string { return m.Text }
func (m *IdentityNode) StringAST() string {
	if m.Quote == 0 {
		return m.Text
	}
	return string(m.Quote) + m.Text + string(m.Quote)
}
func (m *IdentityNode) Check() error        { return nil }
func (m *IdentityNode) NodeType() NodeType  { return IdentityNodeType }
func (m *IdentityNode) Type() reflect.Value { return stringRv }
func (m *IdentityNode) IsBooleanIdentity() bool {
	val := strings.ToLower(m.Text)
	if val == "true" || val == "false" {
		return true
	}
	return false
}
func (m *IdentityNode) Bool() bool {
	val := strings.ToLower(m.Text)
	if val == "true" {
		return true
	}
	return false
}

// Return left, right values if is of form   `table.column` and
// also return true/false for if it even has left/right
func (m *IdentityNode) LeftRight() (string, string, bool) {
	if m.left == "" {
		vals := strings.Split(m.Text, ".")
		if len(vals) == 1 {
			m.left = m.Text
		} else if len(vals) == 2 {
			m.left = vals[0]
			m.right = vals[1]
		} else {
			// ????
			return m.Text, "", false
		}
	}
	return m.left, m.right, m.right != ""
}

func NewNull(operator lex.Token) *NullNode {
	return &NullNode{Pos: Pos(operator.Pos)}
}

func (m *NullNode) String() string      { return "NULL" }
func (m *NullNode) StringAST() string   { return m.String() }
func (n *NullNode) Check() error        { return nil }
func (m *NullNode) NodeType() NodeType  { return NullNodeType }
func (m *NullNode) Type() reflect.Value { return nilRv }

// BinaryNode holds two arguments and an operator
/*
binary_op  = "||" | "&&" | rel_op | add_op | mul_op .
rel_op     = "==" | "!=" | "<" | "<=" | ">" | ">=" .
add_op     = "+" | "-" | "|" | "^" .
mul_op     = "*" | "/" | "%" | "<<" | ">>" | "&" | "&^" .

unary_op   = "+" | "-" | "!" | "^" | "*" | "&" | "<-" .
*/

// Create a Binary node
//   @operator = * + - %/ / && || = ==
//   @operator =  and, or, "is not"
//  @lhArg, rhArg the left, right side of binary
func NewBinaryNode(operator lex.Token, lhArg, rhArg Node) *BinaryNode {
	//u.Debugf("NewBinaryNode: %v %v %v", lhArg, operator, rhArg)
	return &BinaryNode{Pos: Pos(operator.Pos), Args: [2]Node{lhArg, rhArg}, Operator: operator}
}

func (m *BinaryNode) String() string { return m.StringAST() }
func (m *BinaryNode) StringAST() string {
	if m.Paren {
		return fmt.Sprintf("(%s %s %s)", m.Args[0].StringAST(), m.Operator.V, m.Args[1].StringAST())
	}
	return fmt.Sprintf("%s %s %s", m.Args[0].StringAST(), m.Operator.V, m.Args[1].StringAST())
}
func (m *BinaryNode) Check() error {
	// do all args support Binary Operations?   Does that make sense or not?
	return nil
}
func (m *BinaryNode) NodeType() NodeType { return BinaryNodeType }
func (m *BinaryNode) Type() reflect.Value {
	if argVal, ok := m.Args[0].(NodeValueType); ok {
		return argVal.Type()
	}
	return boolRv
}

// A simple binary function is one who does not have nested expressions
//  underneath it, ie just value = y
func (m *BinaryNode) IsSimple() bool {
	for _, arg := range m.Args {
		switch arg.NodeType() {
		case IdentityNodeType, StringNodeType:
			// ok
		default:
			u.Warnf("is not simple: %T", arg)
			return false
		}
	}
	return true
}

// Create a Tri node
//   @operator = Between
//  @arg1, @arg2, @arg3
func NewTriNode(operator lex.Token, arg1, arg2, arg3 Node) *TriNode {
	return &TriNode{Pos: Pos(operator.Pos), Args: [3]Node{arg1, arg2, arg3}, Operator: operator}
}
func (m *TriNode) String() string { return m.StringAST() }
func (m *TriNode) StringAST() string {
	return fmt.Sprintf("%s BETWEEN %s AND %s", m.Args[0].String(), m.Args[1].String(), m.Args[2].StringAST())
}
func (m *TriNode) Check() error        { return nil }
func (m *TriNode) NodeType() NodeType  { return TriNodeType }
func (m *TriNode) Type() reflect.Value { /* ?? */ return boolRv }

func NewUnary(operator lex.Token, arg Node) *UnaryNode {
	return &UnaryNode{Pos: Pos(operator.Pos), Arg: arg, Operator: operator}
}

func (m *UnaryNode) String() string { return fmt.Sprintf("%s%s", m.Operator.V, m.Arg) }
func (m *UnaryNode) StringAST() string {
	if m.Operator.T == lex.TokenNegate {
		return fmt.Sprintf("NOT %s", m.Arg.StringAST())
	}
	return fmt.Sprintf("%s(%s)", m.Operator.V, m.Arg.StringAST())
}
func (n *UnaryNode) Check() error {
	switch t := n.Arg.(type) {
	case Node:
		return t.Check()
	case value.Value:
		//return t.Type()
		return nil
	default:
		return fmt.Errorf("parse: type error in expected? got %v", t)
	}
}
func (m *UnaryNode) NodeType() NodeType  { return UnaryNodeType }
func (m *UnaryNode) Type() reflect.Value { return boolRv }

// Create a Multi Arg node
//   @operator = In
//   @args ....
func NewMultiArgNode(operator lex.Token) *MultiArgNode {
	return &MultiArgNode{Pos: Pos(operator.Pos), Args: make([]Node, 0), Operator: operator}
}
func NewMultiArgNodeArgs(operator lex.Token, args []Node) *MultiArgNode {
	return &MultiArgNode{Pos: Pos(operator.Pos), Args: args, Operator: operator}
}
func (m *MultiArgNode) String() string { return m.StringAST() }
func (m *MultiArgNode) StringAST() string {
	args := make([]string, len(m.Args)-1)
	for i := 1; i < len(m.Args); i++ {
		args[i-1] = m.Args[i].StringAST()
	}
	return fmt.Sprintf("%s %s (%s)", m.Args[0].StringAST(), m.Operator.V, strings.Join(args, ","))
}
func (m *MultiArgNode) Check() error        { return nil }
func (m *MultiArgNode) NodeType() NodeType  { return MultiArgNodeType }
func (m *MultiArgNode) Type() reflect.Value { /* ?? */ return boolRv }
func (m *MultiArgNode) Append(n Node)       { m.Args = append(m.Args, n) }

/*
func NewSetNode(operator lex.Token) *SetNode {
	return &SetNode{Pos: Pos(operator.Pos), Args: make([]Node, 0), Operator: operator}
}

func (m *SetNode) String() string    { return fmt.Sprintf("%s%v", m.Operator.V, m.Args) }
func (m *SetNode) StringAST() string {
	switch {

	return fmt.Sprintf("%s(%v)", m.Operator.V, m.Args)
}
func (n *SetNode) Check() error {
	for _, arg := range n.Args {
		switch t := arg.(type) {
		case Node:
			if err := t.Check(); err != nil {
				return err
			}
		case value.Value:
			continue
		default:
			u.Warnf("unknown type? %T", t)
			return fmt.Errorf("parse: type error in expected? got %v", t)
		}
	}
	return nil
}
func (m *SetNode) NodeType() NodeType  { return SetNodeType }
func (m *SetNode) Type() reflect.Value { return boolRv }
func (m *SetNode) Append(n Node)       { m.Args = append(m.Args, n) }
*/
