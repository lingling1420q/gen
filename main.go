package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"

	_ "github.com/denisenkom/go-mssqldb"
	"github.com/droundy/goopt"
	"github.com/gobuffalo/packd"
	"github.com/gobuffalo/packr/v2"
	"github.com/jimsmart/schema"
	_ "github.com/jinzhu/gorm/dialects/mysql"
	"github.com/jinzhu/inflection"
	_ "github.com/lib/pq"
	_ "github.com/mattn/go-sqlite3"

	"github.com/smallnest/gen/dbmeta"
)

var (
	sqlType         = goopt.String([]string{"--sqltype"}, "mysql", "sql database type such as [ mysql, mssql, postgres, sqlite, etc. ]")
	sqlConnStr      = goopt.String([]string{"-c", "--connstr"}, "nil", "database connection string")
	sqlDatabase     = goopt.String([]string{"-d", "--database"}, "nil", "Database to for connection")
	sqlTable        = goopt.String([]string{"-t", "--table"}, "", "Table to build struct from")
	templateDir     = goopt.String([]string{"--templateDir"}, "", "Template Dir")
	saveTemplateDir = goopt.String([]string{"--save"}, "", "Save templates to dir")

	modelPackageName = goopt.String([]string{"--model"}, "model", "name to set for model package")
	daoPackageName   = goopt.String([]string{"--dao"}, "dao", "name to set for dao package")
	apiPackageName   = goopt.String([]string{"--api"}, "api", "name to set for api package")
	outDir           = goopt.String([]string{"--out"}, ".", "output dir")
	module           = goopt.String([]string{"--module"}, "example.com/example", "module path")
	overwrite        = goopt.Flag([]string{"--overwrite"}, []string{"--no-overwrite"}, "Overwrite existing files (default)", "disable overwriting files")
	contextFileName  = goopt.String([]string{"--context"}, "", "context file (json) to populate context with")
	mappingFileName  = goopt.String([]string{"--mapping"}, "", "mapping file (json) to map sql types to golang/protobuf etc")
	exec             = goopt.String([]string{"--exec"}, "", "execute script for custom code generation")

	AddJSONAnnotation     = goopt.Flag([]string{"--json"}, []string{"--no-json"}, "Add json annotations (default)", "Disable json annotations")
	jsonNameFormat        = goopt.String([]string{"--json-fmt"}, "snake", "json name format [snake | camel | lower_camel | none]")
	AddGormAnnotation     = goopt.Flag([]string{"--gorm"}, []string{}, "Add gorm annotations (tags)", "")
	AddProtobufAnnotation = goopt.Flag([]string{"--protobuf"}, []string{}, "Add protobuf annotations (tags)", "")
	protoNameFormat       = goopt.String([]string{"--proto-fmt"}, "snake", "proto name format [snake | camel | lower_camel | none]")
	AddDBAnnotation       = goopt.Flag([]string{"--db"}, []string{}, "Add db annotations (tags)", "")
	UseGureguTypes        = goopt.Flag([]string{"--guregu"}, []string{}, "Add guregu null types", "")

	copyTemplates    = goopt.Flag([]string{"--copy-templates"}, []string{}, "Copy regeneration templates to project directory", "")
	modGenerate      = goopt.Flag([]string{"--mod"}, []string{}, "Generate go.mod in output dir", "")
	makefileGenerate = goopt.Flag([]string{"--makefile"}, []string{}, "Generate Makefile in output dir", "")
	serverGenerate   = goopt.Flag([]string{"--server"}, []string{}, "Generate server app output dir", "")
	daoGenerate      = goopt.Flag([]string{"--generate-dao"}, []string{}, "Generate dao functions", "")
	projectGenerate  = goopt.Flag([]string{"--generate-proj"}, []string{}, "Generate project readme and gitignore", "")
	restAPIGenerate  = goopt.Flag([]string{"--rest"}, []string{}, "Enable generating RESTful api", "")

	serverHost          = goopt.String([]string{"--host"}, "localhost", "host for server")
	serverPort          = goopt.Int([]string{"--port"}, 8080, "port for server")
	swaggerVersion      = goopt.String([]string{"--swagger_version"}, "1.0", "swagger version")
	swaggerBasePath     = goopt.String([]string{"--swagger_path"}, "/", "swagger base path")
	swaggerTos          = goopt.String([]string{"--swagger_tos"}, "", "swagger tos url")
	swaggerContactName  = goopt.String([]string{"--swagger_contact_name"}, "Me", "swagger contact name")
	swaggerContactURL   = goopt.String([]string{"--swagger_contact_url"}, "http://me.com/terms.html", "swagger contact url")
	swaggerContactEmail = goopt.String([]string{"--swagger_contact_email"}, "me@me.com", "swagger contact email")

	verbose = goopt.Flag([]string{"-v", "--verbose"}, []string{}, "Enable verbose output", "")

	baseTemplates *packr.Box
	tableInfos    map[string]*dbmeta.ModelInfo
)

func init() {
	// Setup goopts
	goopt.Description = func() string {
		return "ORM and RESTful API generator for SQl databases"
	}

	goopt.Version = "0.9.6 (06/06/2020)"
	goopt.Summary = `gen [-v] --sqltype=mysql --connstr "user:password@/dbname" --database <databaseName> --module=example.com/example [--json] [--gorm] [--guregu] [--generate-dao] [--generate-proj]

           sqltype - sql database type such as [ mysql, mssql, postgres, sqlite, etc. ]

`

	//Parse options
	goopt.Parse(nil)

}

func saveTemplates() {
	fmt.Printf("Saving templates to %s\n", *saveTemplateDir)
	err := SaveAssets(*saveTemplateDir, baseTemplates)
	if err != nil {
		fmt.Printf("Error saving: %v\n", err)
	}

}
func listTemplates() {
	for i, file := range baseTemplates.List() {
		fmt.Printf("   [%d] [%s]\n", i, file)
	}
}

func loadContextMapping(conf *dbmeta.Config) {
	contextFile, err := os.Open(*contextFileName)
	if err != nil {
		fmt.Printf("Error loading context file %s error: %v\n", *contextFileName, err)
		return
	}

	defer contextFile.Close()
	jsonParser := json.NewDecoder(contextFile)

	err = jsonParser.Decode(&conf.ContextMap)
	if err != nil {
		fmt.Printf("Error loading context file %s error: %v\n", *contextFileName, err)
		return
	}

	fmt.Printf("Loaded Context from %s with %d defaults\n", *contextFileName, len(conf.ContextMap))
	for key, value := range conf.ContextMap {
		fmt.Printf("    Context:%s -> %s\n", key, value)
	}
}

func main() {

	baseTemplates = packr.New("gen", "./template")

	if *verbose {
		listTemplates()
	}

	if *saveTemplateDir != "" {
		saveTemplates()
		return
	}

	// Username is required
	if sqlConnStr == nil || *sqlConnStr == "" || *sqlConnStr == "nil" {
		fmt.Printf("sql connection string is required! Add it with --connstr=s\n\n")
		fmt.Println(goopt.Usage())
		return
	}

	if sqlDatabase == nil || *sqlDatabase == "" || *sqlDatabase == "nil" {
		fmt.Printf("Database can not be null\n\n")
		fmt.Println(goopt.Usage())
		return
	}

	db, err := initializeDB()
	if err != nil {
		return
	}

	defer db.Close()

	var dbTables []string
	// parse or read tables
	if *sqlTable != "" {
		dbTables = strings.Split(*sqlTable, ",")
	} else {
		dbTables, err = schema.TableNames(db)
		if err != nil {
			fmt.Printf("Error in fetching tables information from %s information schema from %s\n", *sqlType, *sqlConnStr)
			return
		}
	}

	fmt.Printf("Generating code for the following tables (%d)\n", len(dbTables))
	for i, tableName := range dbTables {
		fmt.Printf("[%d] %s\n", i, tableName)
	}

	conf := dbmeta.NewConfig(LoadTemplate)
	initialize(conf)

	err = loadDefaultDBMappings(conf)
	if err != nil {
		fmt.Printf("Error processing default mapping file error: %v\n", err)
		return
	}

	if *mappingFileName != "" {
		err := dbmeta.LoadMappings(*mappingFileName, *verbose)
		if err != nil {
			fmt.Printf("Error loading mappings file %s error: %v\n", *mappingFileName, err)
			return
		}
	}

	if *contextFileName != "" {
		loadContextMapping(conf)
	}

	tableInfos = dbmeta.LoadTableInfo(db, dbTables, conf)
	conf.ContextMap["tableInfos"] = tableInfos

	if *exec != "" {
		executeCustomScript(conf)
		return
	}

	generate(conf)
}

func initializeDB() (db *sql.DB, err error) {

	db, err = sql.Open(*sqlType, *sqlConnStr)
	if err != nil {
		fmt.Printf("Error in open database: %v\n\n", err.Error())
		return nil, err
	}

	err = db.Ping()
	if err != nil {
		fmt.Printf("Error pinging database: %v\n\n", err.Error())
		return
	}

	return
}

func initialize(conf *dbmeta.Config) {
	if outDir == nil || *outDir == "" {
		*outDir = "."
	}

	// if packageName is not set we need to default it
	if modelPackageName == nil || *modelPackageName == "" {
		*modelPackageName = "model"
	}
	if daoPackageName == nil || *daoPackageName == "" {
		*daoPackageName = "dao"
	}
	if apiPackageName == nil || *apiPackageName == "" {
		*apiPackageName = "api"
	}

	conf.SqlType = *sqlType
	conf.SqlDatabase = *sqlDatabase
	conf.ModelPackageName = *modelPackageName
	conf.DaoPackageName = *daoPackageName
	conf.ApiPackageName = *apiPackageName

	conf.AddJSONAnnotation = *AddJSONAnnotation
	conf.AddGormAnnotation = *AddGormAnnotation
	conf.AddProtobufAnnotation = *AddProtobufAnnotation
	conf.AddDBAnnotation = *AddDBAnnotation
	conf.UseGureguTypes = *UseGureguTypes
	conf.JsonNameFormat = *jsonNameFormat
	conf.ProtobufNameFormat = *protoNameFormat
	conf.Verbose = *verbose
	conf.OutDir = *outDir
	conf.Overwrite = *overwrite

	conf.SqlConnStr = *sqlConnStr
	conf.ServerPort = *serverPort
	conf.ServerHost = *serverHost
	conf.Overwrite = *overwrite

	conf.Module = *module
	conf.ModelFQPN = *module + "/" + *modelPackageName
	conf.DaoFQPN = *module + "/" + *daoPackageName
	conf.ApiFQPN = *module + "/" + *apiPackageName

	conf.Swagger.Version = *swaggerVersion
	conf.Swagger.BasePath = *swaggerBasePath
	conf.Swagger.Title = fmt.Sprintf("Sample CRUD api for %s db", *sqlDatabase)
	conf.Swagger.Description = fmt.Sprintf("Sample CRUD api for %s db", *sqlDatabase)
	conf.Swagger.TOS = *swaggerTos
	conf.Swagger.ContactName = *swaggerContactName
	conf.Swagger.ContactURL = *swaggerContactURL
	conf.Swagger.ContactEmail = *swaggerContactEmail
	conf.Swagger.Host = fmt.Sprintf("%s:%d", *serverHost, *serverPort)
}

func loadDefaultDBMappings(conf *dbmeta.Config) error {
	var err error
	var content []byte
	content, err = baseTemplates.Find("mapping.json")
	if err != nil {
		return err
	}

	err = dbmeta.ProcessMappings(content, conf.Verbose)
	if err != nil {
		return err
	}
	return nil
}

func executeCustomScript(conf *dbmeta.Config) {
	fmt.Printf("Executing script %s\n", *exec)

	b, err := ioutil.ReadFile(*exec)
	if err != nil {
		fmt.Printf("Error Loading exec script: %s, error: %v\n", *exec, err)
		return
	}
	content := string(b)
	data := map[string]interface{}{}
	execTemplate(conf, "exec", content, data)
}

func execTemplate(conf *dbmeta.Config, name, templateStr string, data map[string]interface{}) {

	data["DatabaseName"] = *sqlDatabase
	data["module"] = *module
	data["modelFQPN"] = conf.ModelFQPN
	data["daoFQPN"] = conf.DaoFQPN
	data["apiFQPN"] = conf.ApiFQPN
	data["modelPackageName"] = *modelPackageName
	data["daoPackageName"] = *daoPackageName
	data["apiPackageName"] = *apiPackageName
	data["sqlType"] = *sqlType
	data["sqlConnStr"] = *sqlConnStr
	data["serverPort"] = *serverPort
	data["serverHost"] = *serverHost
	data["SwaggerInfo"] = conf.Swagger
	data["tableInfos"] = tableInfos
	data["CommandLine"] = conf.CmdLine
	data["outDir"] = *outDir

	rt, err := conf.GetTemplate(name, templateStr)
	if err != nil {
		fmt.Printf("Error in loading %s template, error: %v\n", name, err)
		return
	}
	var buf bytes.Buffer
	err = rt.Execute(&buf, data)
	if err != nil {
		fmt.Printf("Error in rendering %s: %s\n", name, err.Error())
		return
	}

	fmt.Printf("%s\n", buf.String())
}

func generate(conf *dbmeta.Config) {
	var err error

	*jsonNameFormat = strings.ToLower(*jsonNameFormat)
	modelDir := filepath.Join(*outDir, *modelPackageName)
	apiDir := filepath.Join(*outDir, *apiPackageName)
	daoDir := filepath.Join(*outDir, *daoPackageName)

	err = os.MkdirAll(*outDir, 0777)
	if err != nil && !*overwrite {
		fmt.Printf("unable to create outDir: %s error: %v\n", *outDir, err)
		return
	}

	err = os.MkdirAll(modelDir, 0777)
	if err != nil && !*overwrite {
		fmt.Printf("unable to create modelDir: %s error: %v\n", modelDir, err)
		return
	}

	if *daoGenerate {
		err = os.MkdirAll(daoDir, 0777)
		if err != nil && !*overwrite {
			fmt.Printf("unable to create daoDir: %s error: %v\n", daoDir, err)
			return
		}
	}

	if *restAPIGenerate {
		err = os.MkdirAll(apiDir, 0777)
		if err != nil && !*overwrite {
			fmt.Printf("unable to create apiDir: %s error: %v\n", apiDir, err)
			return
		}
	}
	var ModelTmpl string
	var ModelBaseTmpl string
	var ControllerTmpl string
	var DaoTmpl string
	var DaoFileName string

	var DaoInitTmpl string
	var GoModuleTmpl string

	if ControllerTmpl, err = LoadTemplate("api.go.tmpl"); err != nil {
		fmt.Printf("Error loading template %v\n", err)
		return
	}

	if *AddGormAnnotation {
		DaoFileName = "dao_gorm.go.tmpl"
		if DaoTmpl, err = LoadTemplate(DaoFileName); err != nil {
			fmt.Printf("Error loading template %v\n", err)
			return
		}
		if DaoInitTmpl, err = LoadTemplate("dao_gorm_init.go.tmpl"); err != nil {
			fmt.Printf("Error loading template %v\n", err)
			return
		}
	} else {
		DaoFileName = "dao_sqlx.go.tmpl"
		if DaoTmpl, err = LoadTemplate(DaoFileName); err != nil {
			fmt.Printf("Error loading template %v\n", err)
			return
		}
		if DaoInitTmpl, err = LoadTemplate("dao_sqlx_init.go.tmpl"); err != nil {
			fmt.Printf("Error loading template %v\n", err)
			return
		}
	}

	if GoModuleTmpl, err = LoadTemplate("gomod.tmpl"); err != nil {
		fmt.Printf("Error loading template %v\n", err)
		return
	}

	if ModelTmpl, err = LoadTemplate("model.go.tmpl"); err != nil {
		fmt.Printf("Error loading template %v\n", err)
		return
	}
	if ModelBaseTmpl, err = LoadTemplate("model_base.go.tmpl"); err != nil {
		fmt.Printf("Error loading template %v\n", err)
		return
	}

	*jsonNameFormat = strings.ToLower(*jsonNameFormat)

	// generate go files for each table
	for tableName, tableInfo := range tableInfos {

		if len(tableInfo.Fields) == 0 {
			if *verbose {
				fmt.Printf("[%d] Table: %s - No Fields Available\n", tableInfo.Index, tableName)
			}

			continue
		}

		modelInfo := conf.CreateContextForTableFile(tableInfo)

		modelFile := filepath.Join(modelDir, CreateGoSrcFileName(tableName))
		conf.WriteTemplate("model.go.tmpl", ModelTmpl, modelInfo, modelFile, true)

		if *restAPIGenerate {
			restFile := filepath.Join(apiDir, CreateGoSrcFileName(tableName))
			conf.WriteTemplate("api.go.tmpl", ControllerTmpl, modelInfo, restFile, true)
		}

		if *daoGenerate {
			//write dao
			outputFile := filepath.Join(daoDir, CreateGoSrcFileName(tableName))
			conf.WriteTemplate(DaoFileName, DaoTmpl, modelInfo, outputFile, true)
		}
	}

	data := map[string]interface{}{}

	if *restAPIGenerate {
		if err = generateRestBaseFiles(conf, apiDir); err != nil {
			return
		}
	}

	if *daoGenerate {
		conf.WriteTemplate("daoBase", DaoInitTmpl, data, filepath.Join(daoDir, "dao_base.go"), true)
	}

	conf.WriteTemplate("modelBase", ModelBaseTmpl, data, filepath.Join(modelDir, "model_base.go"), true)

	if *modGenerate {
		conf.WriteTemplate("go.mod", GoModuleTmpl, data, filepath.Join(*outDir, "go.mod"), false)
	}

	if *makefileGenerate {
		if err = generateMakefile(conf); err != nil {
			return
		}
	}

	if *AddProtobufAnnotation {
		if err = generateProtobufDefinitionFile(conf, data); err != nil {
			return
		}
	}

	data = map[string]interface{}{
		"deps":        "go list -f '{{ join .Deps  \"\\n\"}}' .",
		"CommandLine": conf.CmdLine,
	}

	if *projectGenerate {
		if err = generateProjectFiles(conf, data); err != nil {
			return
		}
	}

	if *serverGenerate {
		if err = generateServerCode(conf); err != nil {
			return
		}
	}

	if *copyTemplates {
		if err = copyTemplatesToTarget(); err != nil {
			return
		}
	}
}

func generateRestBaseFiles(conf *dbmeta.Config, apiDir string) (err error) {

	data := map[string]interface{}{}
	var RouterTmpl string
	var HTTPUtilsTmpl string

	if HTTPUtilsTmpl, err = LoadTemplate("http_utils.go.tmpl"); err != nil {
		fmt.Printf("Error loading template %v\n", err)
		return
	}
	if RouterTmpl, err = LoadTemplate("router.go.tmpl"); err != nil {
		fmt.Printf("Error loading template %v\n", err)
		return
	}

	conf.WriteTemplate("router", RouterTmpl, data, filepath.Join(apiDir, "router.go"), true)
	conf.WriteTemplate("example server", HTTPUtilsTmpl, data, filepath.Join(apiDir, "http_utils.go"), true)
	return nil
}

func generateMakefile(conf *dbmeta.Config) (err error) {
	var MakefileTmpl string

	if MakefileTmpl, err = LoadTemplate("Makefile.tmpl"); err != nil {
		fmt.Printf("Error loading template %v\n", err)
		return
	}

	data := map[string]interface{}{
		"deps":         "go list -f '{{ join .Deps  \"\\n\"}}' .",
		"RegenCmdLine": regenCmdLine(),
	}
	conf.WriteTemplate("makefile", MakefileTmpl, data, filepath.Join(*outDir, "Makefile"), false)
	return nil
}

func generateProtobufDefinitionFile(conf *dbmeta.Config, data map[string]interface{}) (err error) {
	var ProtobufTmpl string

	if ProtobufTmpl, err = LoadTemplate("protobuf.tmpl"); err != nil {
		fmt.Printf("Error loading template %v\n", err)
		return err
	}

	protofile := fmt.Sprintf("%s.proto", *sqlDatabase)
	conf.WriteTemplate("protobuf", ProtobufTmpl, data, filepath.Join(*outDir, protofile), false)
	return nil
}

func generateProjectFiles(conf *dbmeta.Config, data map[string]interface{}) (err error) {

	var GitIgnoreTmpl string
	if GitIgnoreTmpl, err = LoadTemplate("gitignore.tmpl"); err != nil {
		fmt.Printf("Error loading template %v\n", err)
		return
	}
	var ReadMeTmpl string
	if ReadMeTmpl, err = LoadTemplate("README.md.tmpl"); err != nil {
		fmt.Printf("Error loading template %v\n", err)
		return
	}

	conf.WriteTemplate("gitignore", GitIgnoreTmpl, data, filepath.Join(*outDir, ".gitignore"), false)
	conf.WriteTemplate("readme", ReadMeTmpl, data, filepath.Join(*outDir, "README.md"), false)
	return nil
}

func generateServerCode(conf *dbmeta.Config) (err error) {
	data := map[string]interface{}{}
	var MainServerTmpl string

	if *AddGormAnnotation {
		if MainServerTmpl, err = LoadTemplate("main_gorm.go.tmpl"); err != nil {
			fmt.Printf("Error loading template %v\n", err)
			return
		}
	} else {
		if MainServerTmpl, err = LoadTemplate("main_sqlx.go.tmpl"); err != nil {
			fmt.Printf("Error loading template %v\n", err)
			return
		}
	}

	serverDir := filepath.Join(*outDir, "app/server")
	err = os.MkdirAll(serverDir, 0777)
	if err != nil {
		fmt.Printf("unable to create serverDir: %s error: %v\n", serverDir, err)
		return
	}
	conf.WriteTemplate("example server", MainServerTmpl, data, filepath.Join(serverDir, "main.go"), true)
	return nil
}

func copyTemplatesToTarget() (err error) {
	templatesDir := filepath.Join(*outDir, "templates")
	err = os.MkdirAll(templatesDir, 0777)
	if err != nil && !*overwrite {
		fmt.Printf("unable to create templatesDir: %s error: %v\n", templatesDir, err)
		return
	}

	fmt.Printf("Saving templates to %s\n", templatesDir)
	err = SaveAssets(templatesDir, baseTemplates)
	if err != nil {
		fmt.Printf("Error saving: %v\n", err)
	}
	return nil
}

func regenCmdLine() string {
	buf := bytes.Buffer{}

	buf.WriteString("gen")
	buf.WriteString(fmt.Sprintf(" --sqltype=%s", *sqlType))
	buf.WriteString(fmt.Sprintf(" --connstr=%s", *sqlConnStr))
	buf.WriteString(fmt.Sprintf(" --database=%s", *sqlDatabase))
	buf.WriteString(fmt.Sprintf(" --templateDir=%s", "./templates"))

	if *sqlTable != "" {
		buf.WriteString(fmt.Sprintf(" --table=%s", *sqlTable))
	}

	buf.WriteString(fmt.Sprintf(" --model=%s", *modelPackageName))
	buf.WriteString(fmt.Sprintf(" --dao=%s", *daoPackageName))
	buf.WriteString(fmt.Sprintf(" --api=%s", *apiPackageName))
	buf.WriteString(fmt.Sprintf(" --out=%s", "./"))
	buf.WriteString(fmt.Sprintf(" --module=%s", *module))
	if *AddJSONAnnotation {
		buf.WriteString(fmt.Sprintf(" --json"))
		buf.WriteString(fmt.Sprintf(" --json-fmt=%s", *jsonNameFormat))
	}
	if *AddGormAnnotation {
		buf.WriteString(fmt.Sprintf(" --gorm"))
	}
	if *AddProtobufAnnotation {
		buf.WriteString(fmt.Sprintf(" --protobuf"))
		buf.WriteString(fmt.Sprintf(" --proto-fmt=%s", *protoNameFormat))
	}
	if *AddDBAnnotation {
		buf.WriteString(fmt.Sprintf(" --db"))
	}
	if *UseGureguTypes {
		buf.WriteString(fmt.Sprintf(" --guregu"))
	}
	if *modGenerate {
		buf.WriteString(fmt.Sprintf(" --mod"))
	}
	if *makefileGenerate {
		buf.WriteString(fmt.Sprintf(" --makefile"))
	}
	if *serverGenerate {
		buf.WriteString(fmt.Sprintf(" --server"))
	}
	if *overwrite {
		buf.WriteString(fmt.Sprintf(" --overwrite"))
	}

	if *contextFileName != "" {
		buf.WriteString(fmt.Sprintf(" --context=%s", *contextFileName))
	}

	buf.WriteString(fmt.Sprintf(" --host=%s", *serverHost))
	buf.WriteString(fmt.Sprintf(" --port=%d", *serverPort))
	if *restAPIGenerate {
		buf.WriteString(fmt.Sprintf(" --rest"))
	}

	if *daoGenerate {
		buf.WriteString(fmt.Sprintf(" --generate-dao"))
	}
	if *projectGenerate {
		buf.WriteString(fmt.Sprintf(" --generate-proj"))
	}

	if *verbose {
		buf.WriteString(fmt.Sprintf(" --verbose"))
	}

	buf.WriteString(fmt.Sprintf(" --swagger_version=%s", *swaggerVersion))
	buf.WriteString(fmt.Sprintf(" --swagger_path=%s", *swaggerBasePath))
	buf.WriteString(fmt.Sprintf(" --swagger_tos=%s", *swaggerTos))
	buf.WriteString(fmt.Sprintf(" --swagger_contact_name=%s", *swaggerContactName))
	buf.WriteString(fmt.Sprintf(" --swagger_contact_url=%s", *swaggerContactURL))
	buf.WriteString(fmt.Sprintf(" --swagger_contact_email=%s", *swaggerContactEmail))

	regenCmdLine := buf.String()
	regenCmdLine = strings.Trim(regenCmdLine, " \t")
	return regenCmdLine
}

// SaveAssets will save the prepacked templates for local editing. File structure will be recreated under the output dir.
func SaveAssets(outputDir string, box *packr.Box) error {
	fmt.Printf("SaveAssets: %v\n", outputDir)
	if outputDir == "" {
		outputDir = "."
	}

	if strings.HasSuffix(outputDir, "/") {
		outputDir = outputDir[:len(outputDir)-1]
	}

	if outputDir == "" {
		outputDir = "."
	}

	_ = box.Walk(func(s string, file packd.File) error {
		fileName := fmt.Sprintf("%s/%s", outputDir, s)

		fi, err := file.FileInfo()
		if err == nil {
			if !fi.IsDir() {

				err := WriteNewFile(fileName, file)
				if err != nil {
					return err
				}
			}
		}
		return nil
	})

	return nil
}

// WriteNewFile will attempt to write a file with the filename and path, a Reader and the FileMode of the file to be created.
// If an error is encountered an error will be returned.
func WriteNewFile(fpath string, in io.Reader) error {
	err := os.MkdirAll(filepath.Dir(fpath), 0775)
	if err != nil {
		return fmt.Errorf("%s: making directory for file: %v", fpath, err)
	}

	out, err := os.Create(fpath)
	if err != nil {
		return fmt.Errorf("%s: creating new file: %v", fpath, err)
	}
	defer func() {
		_ = out.Close()
	}()

	fmt.Printf("WriteNewFile: %s\n", fpath)

	_, err = io.Copy(out, in)
	if err != nil {
		return fmt.Errorf("%s: writing file: %v", fpath, err)
	}
	return nil
}

// CreateGoSrcFileName ensures name doesnt clash with go naming conventions like _test.go
func CreateGoSrcFileName(tableName string) string {
	name := inflection.Singular(tableName)
	if strings.HasSuffix(name, "_test") {
		name = name[0 : len(name)-5]
		name = name + "_tst"
	}
	return name + ".go"
}

func LoadTemplate(filename string) (content string, err error) {
	if *templateDir != "" {
		fpath := filepath.Join(*templateDir, filename)
		var b []byte
		b, err = ioutil.ReadFile(fpath)
		if err == nil {
			fmt.Printf("Loaded template from file: %s\n", fpath)
			content = string(b)
			return content, nil
		}
	}
	content, err = baseTemplates.FindString(filename)
	if err != nil {
		return "", fmt.Errorf("%s not found", filename)
	}
	if *verbose {
		fmt.Printf("Loaded template from app: %s\n", filename)
	}

	return content, nil
}
