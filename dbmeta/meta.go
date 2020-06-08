package dbmeta

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"strings"
	"time"

	"github.com/bxcodec/faker/v3"
	"github.com/iancoleman/strcase"
	"github.com/jinzhu/inflection"
	"github.com/ompluscator/dynamic-struct"
)

type metaDataLoader func(db *sql.DB, sqlType, sqlDatabase, tableName string) (DbTableMeta, error)

var metaDataFuncs = make(map[string]metaDataLoader)
var sqlMappings = make(map[string]*SQLMapping)

func init() {
	metaDataFuncs["sqlite3"] = LoadSqliteMeta
	metaDataFuncs["sqlite"] = LoadSqliteMeta
	metaDataFuncs["mssql"] = LoadMsSQLMeta
	metaDataFuncs["postgres"] = LoadPostgresMeta
	metaDataFuncs["mysql"] = LoadMysqlMeta
}

// SQLMappings mappings for sql types to json, go etc
type SQLMappings struct {
	SQLMappings []*SQLMapping `json:"mappings"`
}

// SQLMapping mapping
type SQLMapping struct {

	// SqlType sql type reported from db
	SQLType string `json:"sql_type"`

	// GoType mapped go type
	GoType string `json:"go_type"`

	// JSONType mapped json type
	JSONType string `json:"json_type"`

	// ProtobufType mapped protobuf type
	ProtobufType string `json:"protobuf_type"`

	// GureguType mapped go type using Guregu
	GureguType string `json:"guregu_type"`

	// GoNullableType mapped go type using nullable
	GoNullableType string `json:"go_nullable_type"`

	// SwaggerType mapped type
	SwaggerType string `json:"swagger_type"`
}

func (m *SQLMapping) String() interface{} {
	return fmt.Sprintf("SQLType: %-15s  GoType: %-15s GureguType: %-15s GoNullableType: %-15s JSONType: %-15s ProtobufType: %-15s",
		m.SQLType,
		m.GoType, m.GureguType, m.GoNullableType,
		m.JSONType, m.ProtobufType)
}

// IsAutoIncrement return is column is a primary key column
func (ci *columnMeta) IsPrimaryKey() bool {
	return ci.isPrimaryKey
}

// IsArray return is column is an array type
func (ci *columnMeta) IsArray() bool {
	return ci.isArray
}

// IsAutoIncrement return is column is an auto increment column
func (ci *columnMeta) IsAutoIncrement() bool {
	return ci.isAutoIncrement
}

type columnMeta struct {
	index           int
	ct              *sql.ColumnType
	nullable        bool
	isPrimaryKey    bool
	isAutoIncrement bool
	isArray         bool
	colDDL          string
	columnType      string
	columnLen       int64
	defaultVal      string
	notes           string
}

// ColumnType column type
func (ci *columnMeta) ColumnType() string {
	return ci.columnType
}

// Notes notes on column generation
func (ci *columnMeta) Notes() string {
	return ci.notes
}

// ColumnLength column length for text or varhar
func (ci *columnMeta) ColumnLength() int64 {
	return ci.columnLen
}

// DefaultValue default value of column
func (ci *columnMeta) DefaultValue() string {
	return ci.defaultVal
}

// Name name of column
func (ci *columnMeta) Name() string {
	return ci.ct.Name()
}

// Index index of column in db
func (ci *columnMeta) Index() int {
	return ci.index
}

// String friendly string for columnMeta
func (ci *columnMeta) String() string {
	return fmt.Sprintf("[%2d] %-45s  %-20s null: %-6t primary: %-6t isArray: %-6t auto: %-6t col: %-15s len: %-7d default: [%s]",
		ci.index, ci.ct.Name(), ci.DatabaseTypePretty(),
		ci.nullable, ci.isPrimaryKey, ci.isArray,
		ci.isAutoIncrement, ci.columnType, ci.columnLen, ci.defaultVal)
}

// Nullable reports whether the column may be null.
// If a driver does not support this property ok will be false.
func (ci *columnMeta) Nullable() bool {
	return ci.nullable
}

// ColDDL string of the ddl for the column
func (ci *columnMeta) ColDDL() string {
	return ci.colDDL
}

// DatabaseTypeName returns the database system name of the column type. If an empty
// string is returned the driver type name is not supported.
// Consult your driver documentation for a list of driver data types. Length specifiers
// are not included.
// Common type include "VARCHAR", "TEXT", "NVARCHAR", "DECIMAL", "BOOL", "INT", "BIGINT".
func (ci *columnMeta) DatabaseTypeName() string {
	return ci.ct.DatabaseTypeName()
}

// DatabaseTypePretty string of the db type
func (ci *columnMeta) DatabaseTypePretty() string {
	if ci.columnLen > 0 {
		return fmt.Sprintf("%s(%d)", ci.columnType, ci.columnLen)
	}

	return ci.columnType
}

// DbTableMeta table meta data
type DbTableMeta interface {
	Columns() []ColumnMeta
	SQLType() string
	SQLDatabase() string
	TableName() string
	DDL() string
}

// ColumnMeta meta data for a column
type ColumnMeta interface {
	Name() string
	String() string
	Nullable() bool
	DatabaseTypeName() string
	DatabaseTypePretty() string
	Index() int
	IsPrimaryKey() bool
	IsAutoIncrement() bool
	IsArray() bool
	ColumnType() string
	Notes() string
	ColumnLength() int64
	DefaultValue() string
}

type dbTableMeta struct {
	sqlType       string
	sqlDatabase   string
	tableName     string
	columns       []*columnMeta
	ddl           string
	primaryKeyPos int
}

// PrimaryKeyPos ordinal pos of primary key
func (m *dbTableMeta) PrimaryKeyPos() int {
	return m.primaryKeyPos
}

// SQLType sql db type
func (m *dbTableMeta) SQLType() string {
	return m.sqlType
}

// SQLDatabase sql database name
func (m *dbTableMeta) SQLDatabase() string {
	return m.sqlDatabase
}

// TableName sql table name
func (m *dbTableMeta) TableName() string {
	return m.tableName
}

// Columns ColumnMeta for columns in a sql table
func (m *dbTableMeta) Columns() []ColumnMeta {

	cols := make([]ColumnMeta, len(m.columns))
	for i, v := range m.columns {
		cols[i] = ColumnMeta(v)
	}
	return cols
}

// DDL string for a sql table
func (m *dbTableMeta) DDL() string {
	return m.ddl
}

// ModelInfo info for a sql table
type ModelInfo struct {
	Index           int
	IndexPlus1      int
	PackageName     string
	StructName      string
	ShortStructName string
	TableName       string
	Fields          []string
	DBMeta          DbTableMeta
	Instance        interface{}
	CodeFields      []*FieldInfo
}

// Notes notes on table generation
func (m *ModelInfo) Notes() string {
	buf := bytes.Buffer{}

	for i, j := range m.DBMeta.Columns() {
		if j.Notes() != "" {
			buf.WriteString(fmt.Sprintf("[%2d] %s\n", i, j.Notes()))
		}
	}

	for i, j := range m.CodeFields {
		if j.Notes != "" {
			buf.WriteString(fmt.Sprintf("[%2d] %s\n", i, j.Notes))
		}
	}

	return buf.String()
}

// FieldInfo codegen info for each column in sql table
type FieldInfo struct {
	Index                 int
	GoFieldName           string
	GoFieldType           string
	GoAnnotations         []string
	JSONFieldName         string
	ProtobufFieldName     string
	ProtobufType          string
	ProtobufPos           int
	Comment               string
	Notes                 string
	Code                  string
	FakeData              interface{}
	ColumnMeta            ColumnMeta
	PrimaryKeyFieldParser string
	PrimaryKeyArgName     string
	SqlMapping            *SQLMapping
}

// LoadMeta loads the DbTableMeta data from the db connection for the table
func LoadMeta(sqlType string, db *sql.DB, sqlDatabase, tableName string) (DbTableMeta, error) {
	dbMetaFunc, haveMeta := metaDataFuncs[sqlType]
	if !haveMeta {
		dbMetaFunc = LoadUnknownMeta
	}

	dbMeta, err := dbMetaFunc(db, sqlType, sqlDatabase, tableName)
	return dbMeta, err
}

func checkDupeFieldName(fields []*FieldInfo, fieldName string) string {
	var match bool
	for _, field := range fields {
		if fieldName == field.GoFieldName {
			match = true
			break
		}
	}

	if match {
		return fmt.Sprintf("%s_", fieldName)
	}

	return fieldName
}

// Generate fields string
func (c *Config) GenerateFieldsTypes(dbMeta DbTableMeta) ([]*FieldInfo, error) {

	var fields []*FieldInfo
	field := ""
	for i, col := range dbMeta.Columns() {
		name := col.Name()

		valueType, err := SQLTypeToGoType(strings.ToLower(col.DatabaseTypeName()), col.Nullable(), c.UseGureguTypes)
		if err != nil { // unknown type
			fmt.Printf("table: %s unable to generate struct field: %s type: %s error: %v\n", dbMeta.TableName(), name, col.DatabaseTypeName(), err)
			continue
		}

		fieldName := FmtFieldName(stringifyFirstChar(name))
		fieldName = checkDupeFieldName(fields, fieldName)

		var annotations []string
		if c.AddGormAnnotation {
			annotations = append(annotations, createGormAnnotation(col))
		}

		if c.AddJSONAnnotation {
			annotations = append(annotations, createJSONAnnotation(c.JsonNameFormat, col))
		}

		if c.AddDBAnnotation {
			annotations = append(annotations, createDBAnnotation(col))
		}

		if c.AddProtobufAnnotation {
			annnotation, err := createProtobufAnnotation(c.ProtobufNameFormat, col)
			if err == nil {
				annotations = append(annotations, annnotation)
			}
		}

		if len(annotations) > 0 {
			field = fmt.Sprintf("%s %s `%s`",
				fieldName,
				valueType,
				strings.Join(annotations, " "))
		} else {
			field = fmt.Sprintf("%s %s", fieldName, valueType)
		}

		field = fmt.Sprintf("//%s\n    %s", col.String(), field)

		sqlMapping, _ := SQLTypeToMapping(strings.ToLower(col.DatabaseTypeName()))
		goType, _ := SQLTypeToGoType(strings.ToLower(col.DatabaseTypeName()), false, false)
		protobufType, _ := SQLTypeToProtobufType(col.DatabaseTypeName())
		fakeData := createFakeData(goType, fieldName)

		//if c.Verbose {
		//	fmt.Printf("table: %-10s type: %-10s fieldname: %-20s val: %v\n", c.DatabaseTypeName(), goType, fieldName, fakeData)
		//	spew.Dump(fakeData)
		//}

		//fmt.Printf("%+v", fakeData)
		primaryKeyFieldParser := ""
		if col.IsPrimaryKey() {
			var ok bool
			primaryKeyFieldParser, ok = parsePrimaryKeys[goType]
			if !ok {
				primaryKeyFieldParser = "unsupported"
			}
		}

		fi := &FieldInfo{
			Index:                 i,
			Code:                  field,
			GoFieldName:           fieldName,
			GoFieldType:           valueType,
			GoAnnotations:         annotations,
			FakeData:              fakeData,
			Comment:               col.String(),
			JSONFieldName:         formatFieldName(c.JsonNameFormat, col),
			ProtobufFieldName:     formatFieldName(c.ProtobufNameFormat, col),
			ProtobufType:          protobufType,
			ProtobufPos:           i + 1,
			ColumnMeta:            col,
			PrimaryKeyFieldParser: primaryKeyFieldParser,
			SqlMapping:            sqlMapping,
		}

		fields = append(fields, fi)
	}
	return fields, nil
}

func formatFieldName(nameFormat string, c ColumnMeta) string {

	var jsonName string
	switch nameFormat {
	case "snake":
		jsonName = strcase.ToSnake(c.Name())
	case "camel":
		jsonName = strcase.ToCamel(c.Name())
	case "lower_camel":
		jsonName = strcase.ToLowerCamel(c.Name())
	case "none":
		jsonName = c.Name()
	default:
		jsonName = c.Name()
	}
	return jsonName
}

func createJSONAnnotation(nameFormat string, c ColumnMeta) string {

	name := formatFieldName(nameFormat, c)
	return fmt.Sprintf("json:\"%s\"", name)
}

func createDBAnnotation(c ColumnMeta) string {
	return fmt.Sprintf("db:\"%s\"", c.Name())
}

func createProtobufAnnotation(nameFormat string, c ColumnMeta) (string, error) {
	protoBufType, err := SQLTypeToProtobufType(c.DatabaseTypeName())
	if err != nil {
		return "", err
	}

	if protoBufType != "" {
		name := formatFieldName(nameFormat, c)
		return fmt.Sprintf("protobuf:\"%s,%d,opt,name=%s\"", protoBufType, c.Index(), name), nil
	}

	return "", fmt.Errorf("unknown sql name: %s", c.Name())
}

func createGormAnnotation(c ColumnMeta) string {
	buf := bytes.Buffer{}

	key := c.Name()
	buf.WriteString("gorm:\"")

	if c.IsPrimaryKey() {
		buf.WriteString("primary_key;")
	}
	if c.IsAutoIncrement() {
		buf.WriteString("AUTO_INCREMENT;")
	}

	buf.WriteString("column:")
	buf.WriteString(key)
	buf.WriteString(";")

	if c.DatabaseTypeName() != "" {
		buf.WriteString("type:")
		buf.WriteString(c.DatabaseTypeName())
		buf.WriteString(";")

		if c.ColumnLength() > 0 {
			buf.WriteString(fmt.Sprintf("size:%d;", c.ColumnLength()))
		}

		if c.DefaultValue() != "" {
			value := c.DefaultValue()
			value = strings.Replace(value, "\"", "'", -1)

			if value == "NULL" || value == "null" {
				value = ""
			}

			if value != "" && !strings.Contains(value, "()") {
				buf.WriteString(fmt.Sprintf("default:%s;", value))
			}
		}

	}

	buf.WriteString("\"")
	return buf.String()
}

// BuildDefaultTableDDL create a ddl mock using the ColumnMeta data
func BuildDefaultTableDDL(tableName string, cols []*columnMeta) string {
	buf := bytes.Buffer{}
	buf.WriteString(fmt.Sprintf("Table: %s\n", tableName))

	for _, ct := range cols {
		buf.WriteString(fmt.Sprintf("%s\n", ct.String()))
	}
	return buf.String()
}

// ProcessMappings process the json for mappings to load sql mappings
func ProcessMappings(mappingJsonstring []byte, verbose bool) error {
	var mappings = &SQLMappings{}
	err := json.Unmarshal(mappingJsonstring, mappings)
	if err != nil {
		fmt.Printf("Error unmarshalling json error: %v\n", err)
		return err
	}

	if verbose {
		fmt.Printf("Loaded %d mappings\n", len(mappings.SQLMappings))
	}
	for i, value := range mappings.SQLMappings {
		if verbose {
			fmt.Printf("    Mapping:[%2d] -> %s\n", i, value.SQLType)
		}

		sqlMappings[value.SQLType] = value
	}
	return nil
}

// LoadMappings load sql mappings to load mapping json file
func LoadMappings(mappingFileName string, verbose bool) error {
	mappingFile, err := os.Open(mappingFileName)
	if err != nil {
		fmt.Printf("Error loading mapping file %s error: %v\n", mappingFileName, err)
		return err
	}

	defer func() {
		_ = mappingFile.Close()
	}()
	byteValue, err := ioutil.ReadAll(mappingFile)
	if err != nil {
		fmt.Printf("Error loading mapping file %s error: %v\n", mappingFileName, err)
		return err
	}
	return ProcessMappings(byteValue, verbose)
}

// SQLTypeToGoType map a sql type to a go type
func SQLTypeToGoType(sqlType string, nullable bool, gureguTypes bool) (string, error) {
	mapping, err := SQLTypeToMapping(sqlType)
	if err != nil {
		return "", err
	}

	if nullable && gureguTypes {
		return mapping.GureguType, nil
	} else if nullable {
		return mapping.GoNullableType, nil
	} else {
		return mapping.GoType, nil
	}
}

// SQLTypeToProtobufType map a sql type to a protobuf type
func SQLTypeToProtobufType(sqlType string) (string, error) {
	mapping, err := SQLTypeToMapping(sqlType)
	if err != nil {
		return "", err
	}
	return mapping.ProtobufType, nil
}

// SQLTypeToMapping retrieve a SqlMapping based on a sql type
func SQLTypeToMapping(sqlType string) (*SQLMapping, error) {
	sqlType = cleanupSQLType(sqlType)

	mapping, ok := sqlMappings[sqlType]
	if !ok {
		return nil, fmt.Errorf("unknown sql type: %s", sqlType)
	}
	return mapping, nil
}

func cleanupSQLType(sqlType string) string {
	sqlType = strings.ToLower(sqlType)
	sqlType = strings.Trim(sqlType, " \t")
	sqlType = strings.ToLower(sqlType)
	idx := strings.Index(sqlType, "(")
	if idx > -1 {
		sqlType = sqlType[0:idx]
	}
	return sqlType
}

// GetMappings get all mappings
func GetMappings() map[string]*SQLMapping {
	return sqlMappings
}

func createFakeData(valueType string, name string) interface{} {

	switch valueType {
	case "[]byte":
		return []byte("hello world")
	case "bool":
		return true
	case "float32":
		return float32(1.0)
	case "float64":
		return float64(1.0)
	case "int":
		return int(1)
	case "int64":
		return int64(1)
	case "string":
		return "hello world"
	case "time.Time":
		return time.Now()
	case "interface{}":
		return 1
	default:
		return 1
	}

}

func LoadTableInfo(db *sql.DB, dbTables []string, conf *Config) map[string]*ModelInfo {

	tableInfos := make(map[string]*ModelInfo)

	// generate go files for each table
	var tableIdx = 0
	for i, tableName := range dbTables {
		if strings.HasPrefix(tableName, "[") && strings.HasSuffix(tableName, "]") {
			tableName = tableName[1 : len(tableName)-1]
		}

		dbMeta, err := LoadMeta(conf.SqlType, db, conf.SqlDatabase, tableName)
		if err != nil {
			fmt.Printf("Error getting table info for %s error: %v\n", tableName, err)
			continue
		}

		modelInfo, err := GenerateModelInfo(dbMeta, tableName, conf)
		if err != nil {
			fmt.Printf("Error getting table info for %s error: %v\n", tableName, err)
			continue
		}

		if len(modelInfo.Fields) == 0 {
			if conf.Verbose {
				fmt.Printf("[%d] Table: %s - No Fields Available\n", i, tableName)
			}
			continue
		}

		modelInfo.Index = tableIdx
		modelInfo.IndexPlus1 = tableIdx + 1
		tableIdx++

		tableInfos[tableName] = modelInfo
	}

	return tableInfos
}

// GenerateModelInfo generates a struct for the given table.
func GenerateModelInfo(dbMeta DbTableMeta,
	tableName string,
	conf *Config) (*ModelInfo, error) {

	structName := FmtFieldName(tableName)
	structName = inflection.Singular(structName)

	conf.JsonNameFormat = strings.ToLower(conf.JsonNameFormat)

	fields, err := conf.GenerateFieldsTypes(dbMeta)
	if err != nil {
		return nil, err
	}

	if conf.Verbose {
		fmt.Printf("tableName: %s\n", tableName)
		for _, c := range dbMeta.Columns() {
			fmt.Printf("    %s\n", c.String())
		}
	}

	generator := dynamicstruct.NewStruct()

	noOfPrimaryKeys := 0
	for i, c := range fields {
		meta := dbMeta.Columns()[i]
		jsonName := formatFieldName(conf.JsonNameFormat, meta)
		tag := fmt.Sprintf(`json:"%s"`, jsonName)
		fakeData := c.FakeData
		generator = generator.AddField(c.GoFieldName, fakeData, tag)
		if meta.IsPrimaryKey() {
			//c.PrimaryKeyArgName = RenameReservedName(strcase.ToLowerCamel(c.GoFieldName))
			c.PrimaryKeyArgName = fmt.Sprintf("arg%s", strcase.ToCamel(c.GoFieldName))
			noOfPrimaryKeys++
		}
	}

	instance := generator.Build().New()

	err = faker.FakeData(&instance)
	if err != nil {
		fmt.Println(err)
	}
	// fmt.Printf("%+v", instance)

	var code []string
	for _, f := range fields {

		if f.PrimaryKeyFieldParser == "unsupported" {
			return nil, fmt.Errorf("unable to generate code for table: %s, primary key column: [%d] %s has unsupported type: %s / %s",
				dbMeta.TableName(), f.ColumnMeta.Index(), f.ColumnMeta.Name(), f.ColumnMeta.DatabaseTypeName(), f.GoFieldType)
		}
		code = append(code, f.Code)
	}

	var modelInfo = &ModelInfo{
		PackageName:     conf.ModelPackageName,
		StructName:      structName,
		TableName:       tableName,
		ShortStructName: strings.ToLower(string(structName[0])),
		Fields:          code,
		CodeFields:      fields,
		DBMeta:          dbMeta,
		Instance:        instance,
	}

	return modelInfo, nil
}
