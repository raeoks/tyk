package main

import (
	"encoding/json"
	"errors"
	"io/ioutil"
	"strings"

	"github.com/satori/go.uuid"

	"github.com/TykTechnologies/tyk/apidef"
)

const SwaggerSource APIImporterSource = "swagger"

type DefinitionObjectFormatAST struct {
	Format string `json:"format"`
	Type   string `json:"type"`
}

type DefinitionObjectAST struct {
	Type       string                               `json:"type"`
	Required   []string                             `json:"required"`
	Properties map[string]DefinitionObjectFormatAST `json:"properties"`
}

type ResponseCodeObjectAST struct {
	Description string `json:"description"`
	Schema      struct {
		Items map[string]interface{} `json:"items"`
		Type  string                 `json:"type"`
	} `json:"schema"`
}

type PathMethodObject struct {
	Description string                           `json:"description"`
	OperationID string                           `json:"operationId"`
	Responses   map[string]ResponseCodeObjectAST `json:"responses"`
}

type PathItemObject struct {
	Get     PathMethodObject `json:"get"`
	Put     PathMethodObject `json:"put"`
	Post    PathMethodObject `json:"post"`
	Patch   PathMethodObject `json:"patch"`
	Options PathMethodObject `json:"options"`
	Delete  PathMethodObject `json:"delete"`
	Head    PathMethodObject `json:"head"`
}

type SwaggerAST struct {
	BasePath    string                         `json:"basePath"`
	Consumes    []string                       `json:"consumes"`
	Definitions map[string]DefinitionObjectAST `json:"definitions"`
	Host        string                         `json:"host"`
	Info        struct {
		Contact struct {
			Email string `json:"email"`
			Name  string `json:"name"`
			URL   string `json:"url"`
		} `json:"contact"`
		Description string `json:"description"`
		License     struct {
			Name string `json:"name"`
			URL  string `json:"url"`
		} `json:"license"`
		TermsOfService string `json:"termsOfService"`
		Title          string `json:"title"`
		Version        string `json:"version"`
	} `json:"info"`
	Paths    map[string]PathItemObject `json:"paths"`
	Produces []string                  `json:"produces"`
	Schemes  []string                  `json:"schemes"`
	Swagger  string                    `json:"swagger"`
}

func (s *SwaggerAST) ReadString(asJson string) error {
	if err := json.Unmarshal([]byte(asJson), &s); err != nil {
		log.Error("Marshalling failed: ", err)
		return err
	}
	return nil
}

func (s *SwaggerAST) ConvertIntoApiVersion(asMock bool) (apidef.VersionInfo, error) {
	versionInfo := apidef.VersionInfo{}

	if asMock {
		return versionInfo, errors.New("Swagger mocks not supported")
	}

	versionInfo.UseExtendedPaths = true
	versionInfo.Name = s.Info.Version
	versionInfo.ExtendedPaths.WhiteList = make([]apidef.EndPointMeta, 0)

	if len(s.Paths) == 0 {
		return versionInfo, errors.New("no paths defined in swagger file")
	}
	for pathName, pathSpec := range s.Paths {
		log.Debug("path: %s", pathName)
		newEndpointMeta := apidef.EndPointMeta{}
		newEndpointMeta.MethodActions = make(map[string]apidef.EndpointMethodMeta)
		newEndpointMeta.Path = pathName

		// We just want the paths here, no mocks
		methods := map[string]PathMethodObject{
			"GET":     pathSpec.Get,
			"PUT":     pathSpec.Put,
			"POST":    pathSpec.Post,
			"HEAD":    pathSpec.Head,
			"PATCH":   pathSpec.Patch,
			"OPTIONS": pathSpec.Options,
			"DELETE":  pathSpec.Delete,
		}
		for methodName, m := range methods {
			// skip methods that are not defined
			if len(m.Responses) == 0 && m.Description == "" && m.OperationID == "" {
				continue
			}
			methodAction := apidef.EndpointMethodMeta{}
			methodAction.Action = apidef.NoAction
			newEndpointMeta.MethodActions[methodName] = methodAction
		}

		versionInfo.ExtendedPaths.WhiteList = append(versionInfo.ExtendedPaths.WhiteList, newEndpointMeta)
	}

	return versionInfo, nil
}

func (s *SwaggerAST) InsertIntoAPIDefinitionAsVersion(version apidef.VersionInfo, def *apidef.APIDefinition, versionName string) error {
	def.VersionData.NotVersioned = false
	def.VersionData.Versions[versionName] = version
	return nil
}

// Comand mode stuff

func handleSwaggerMode(arguments map[string]interface{}) {
	doCreate := arguments["--create-api"]
	inputFile := arguments["--import-swagger"]
	if doCreate == true {
		upstreamVal := arguments["--upstream-target"]
		orgId := arguments["--org-id"]
		if upstreamVal != nil && orgId != nil {
			// Create the API with the blueprint
			s, err := swaggerLoadFile(inputFile.(string))
			if err != nil {
				log.Error("File load error: ", err)
				return
			}

			def, err := createDefFromSwagger(s, orgId.(string), upstreamVal.(string), arguments["--as-mock"].(bool))
			if err != nil {
				log.Error("Failed to create API Defintition from file")
				return
			}

			printDef(def)
			return
		}

		log.Error("No upstream target or org ID defined, these are both required")

	} else {
		// Different branch, here we need an API Definition to modify
		forApiPath := arguments["--for-api"]
		if forApiPath == nil {
			log.Error("If ading to an API, the path to the defintiton must be listed")
			return
		}

		versionName := arguments["--as-version"]
		if versionName == nil {
			log.Error("No version defined for this import operation, please set an import ID using the --as-version flag")
			return
		}

		defFromFile, err := apiDefLoadFile(forApiPath.(string))
		if err != nil {
			log.Error("failed to load and decode file data for API Definition: ", err)
			return
		}

		s, err := swaggerLoadFile(inputFile.(string))
		if err != nil {
			log.Error("File load error: ", err)
			return
		}

		versionData, err := s.ConvertIntoApiVersion(arguments["--as-mock"].(bool))
		if err != nil {
			log.Error("Conversion into API Def failed: ", err)
		}

		if err := s.InsertIntoAPIDefinitionAsVersion(versionData, defFromFile, versionName.(string)); err != nil {
			log.Error("Insertion failed: ", err)
			return
		}

		printDef(defFromFile)

	}
}

func createDefFromSwagger(s *SwaggerAST, orgId, upstreamURL string, as_mock bool) (*apidef.APIDefinition, error) {
	ad := apidef.APIDefinition{
		Name:             s.Info.Title,
		Active:           true,
		UseKeylessAccess: true,
		APIID:            uuid.NewV4().String(),
		OrgID:            orgId,
	}
	ad.VersionDefinition.Key = "version"
	ad.VersionDefinition.Location = "header"
	ad.VersionData.Versions = make(map[string]apidef.VersionInfo)
	ad.Proxy.ListenPath = "/" + ad.APIID + "/"
	ad.Proxy.StripListenPath = true
	ad.Proxy.TargetURL = upstreamURL

	if as_mock {
		log.Warning("Mocks not supported for Swagger definitions, ignoring option")
	}
	versionData, err := s.ConvertIntoApiVersion(false)
	if err != nil {
		log.Error("Conversion into API Def failed: ", err)
	}

	s.InsertIntoAPIDefinitionAsVersion(versionData, &ad, strings.Trim(s.Info.Version, " "))

	return &ad, nil
}

func swaggerLoadFile(filePath string) (*SwaggerAST, error) {
	swagger, err := GetImporterForSource(SwaggerSource)
	if err != nil {
		log.Error("Couldn't get swagger importer: ", err)
		return nil, err
	}

	swaggerFileData, err := ioutil.ReadFile(filePath)
	if err != nil {
		log.Error("Couldn't load swagger file: ", err)
		return nil, err
	}

	if err := swagger.ReadString(string(swaggerFileData)); err != nil {
		log.Error("Failed to decode object")
		return nil, err
	}

	return swagger.(*SwaggerAST), nil
}
