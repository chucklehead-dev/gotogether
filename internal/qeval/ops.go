package qeval

// Kinds and opcodes as integers (I39), set when the IR is read
// (Expr.UnmarshalJSON): the evaluator switches on them, not on the IR's
// strings. A kind or opcode outside these is kindOther or opOther, and
// the evaluator refuses it as it refused the string.

type exprKind uint8

const (
	kindOther exprKind = iota
	kindInt
	kindBool
	kindStr
	kindName
	kindLambda
	kindLet
	kindApp
)

// kinds are the IR's kinds by name, read-only
var kinds = map[string]exprKind{
	"int": kindInt, "bool": kindBool, "str": kindStr, "name": kindName,
	"lambda": kindLambda, "let": kindLet, "app": kindApp,
}

type opcode uint8

const (
	opOther opcode = iota
	opAnd
	opOr
	opNot
	opImplies
	opIff
	opEq
	opNeq
	opIte
	opAssert
	opIadd
	opIsub
	opImul
	opIdiv
	opImod
	opIpow
	opIuminus
	opIgt
	opIgte
	opIlt
	opIlte
	opSetCons
	opTo
	opContains
	opIn
	opUnion
	opIntersect
	opExclude
	opSubseteq
	opSize
	opIsFinite
	opFilter
	opSelect
	opMap
	opExists
	opForall
	opFold
	opFlatten
	opPowerset
	opTuples
	opChooseSome
	opOneOf
	opMapCons
	opMapBy
	opSetToMap
	opKeys
	opGet
	opSet
	opPut
	opSetBy
	opRecCons
	opField
	opWith
	opFieldNames
	opTupCons
	opItem
	opVariant
	opMatchVariant
	opListCons
	opAppend
	opConcat
	opHead
	opTail
	opLength
	opNth
	opReplaceAt
	opSlice
	opRange
	opIndices
	opFoldl
	opFoldr
	opAssign
	opActionAll
	opActionAny
)

// opcodes are the builtins the evaluator knows, and the actions' own,
// by name; read-only
var opcodes = map[string]opcode{
	"and":          opAnd,
	"or":           opOr,
	"not":          opNot,
	"implies":      opImplies,
	"iff":          opIff,
	"eq":           opEq,
	"neq":          opNeq,
	"ite":          opIte,
	"assert":       opAssert,
	"iadd":         opIadd,
	"isub":         opIsub,
	"imul":         opImul,
	"idiv":         opIdiv,
	"imod":         opImod,
	"ipow":         opIpow,
	"iuminus":      opIuminus,
	"igt":          opIgt,
	"igte":         opIgte,
	"ilt":          opIlt,
	"ilte":         opIlte,
	"Set":          opSetCons,
	"to":           opTo,
	"contains":     opContains,
	"in":           opIn,
	"union":        opUnion,
	"intersect":    opIntersect,
	"exclude":      opExclude,
	"subseteq":     opSubseteq,
	"size":         opSize,
	"isFinite":     opIsFinite,
	"filter":       opFilter,
	"select":       opSelect,
	"map":          opMap,
	"exists":       opExists,
	"forall":       opForall,
	"fold":         opFold,
	"flatten":      opFlatten,
	"powerset":     opPowerset,
	"tuples":       opTuples,
	"chooseSome":   opChooseSome,
	"oneOf":        opOneOf,
	"Map":          opMapCons,
	"mapBy":        opMapBy,
	"setToMap":     opSetToMap,
	"keys":         opKeys,
	"get":          opGet,
	"set":          opSet,
	"put":          opPut,
	"setBy":        opSetBy,
	"Rec":          opRecCons,
	"field":        opField,
	"with":         opWith,
	"fieldNames":   opFieldNames,
	"Tup":          opTupCons,
	"item":         opItem,
	"variant":      opVariant,
	"matchVariant": opMatchVariant,
	"List":         opListCons,
	"append":       opAppend,
	"concat":       opConcat,
	"head":         opHead,
	"tail":         opTail,
	"length":       opLength,
	"nth":          opNth,
	"replaceAt":    opReplaceAt,
	"slice":        opSlice,
	"range":        opRange,
	"indices":      opIndices,
	"foldl":        opFoldl,
	"foldr":        opFoldr,
	"assign":       opAssign,
	"actionAll":    opActionAll,
	"actionAny":    opActionAny,
}
