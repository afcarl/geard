package http

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"github.com/smarterclayton/geard/dispatcher"
	"github.com/smarterclayton/geard/gears"
	"github.com/smarterclayton/geard/jobs"
	"github.com/smarterclayton/go-json-rest"
	"io"
	"log"
	"net/http"
	"sync"
)

var ErrHandledResponse = errors.New("Request handled")

func StartAPI(wg *sync.WaitGroup, dispatch *dispatcher.Dispatcher) error {
	wg.Add(1)
	go func() {
		defer wg.Done()

		connect := ":8080"
		log.Printf("Starting HTTP on %s ... ", connect)
		http.Handle("/", newHttpApiHandler(dispatch))
		log.Fatal(http.ListenAndServe(connect, nil))
	}()
	return nil
}

func newHttpApiHandler(dispatch *dispatcher.Dispatcher) *rest.ResourceHandler {
	handler := rest.ResourceHandler{
		EnableRelaxedContentType: true,
		EnableResponseStackTrace: true,
		EnableGzip:               false,
	}
	handler.SetRoutes(
		rest.Route{"GET", "/token/:token/images", jobRestHandler(dispatch, apiListImages)},
		rest.Route{"GET", "/token/:token/containers", jobRestHandler(dispatch, apiListContainers)},
		rest.Route{"PUT", "/token/:token/container", jobRestHandler(dispatch, apiPutContainer)},
		rest.Route{"GET", "/token/:token/container/log", jobRestHandler(dispatch, apiGetContainerLog)},
		rest.Route{"PUT", "/token/:token/container/:action", jobRestHandler(dispatch, apiPutContainerAction)},
		rest.Route{"PUT", "/token/:token/repository", jobRestHandler(dispatch, apiPutRepository)},
		rest.Route{"PUT", "/token/:token/keys", jobRestHandler(dispatch, apiPutKeys)},
		rest.Route{"GET", "/token/:token/content", jobRestHandler(dispatch, apiGetContent)},
		rest.Route{"GET", "/token/:token/content/*", jobRestHandler(dispatch, apiGetContent)},
		rest.Route{"PUT", "/token/:token/build-image", jobRestHandler(dispatch, apiPutBuildImageAction)},
		rest.Route{"PUT", "/token/:token/environment", jobRestHandler(dispatch, apiPutEnvironment)},
		rest.Route{"PATCH", "/token/:token/environment", jobRestHandler(dispatch, apiPatchEnvironment)},
		rest.Route{"PUT", "/token/:token/linkcontainers", jobRestHandler(dispatch, apiLinkContainers)},
	)
	return &handler
}

type jobHandler func(jobs.RequestIdentifier, *TokenData, *rest.ResponseWriter, *rest.Request) (jobs.Job, error)

func jobRestHandler(dispatch *dispatcher.Dispatcher, handler jobHandler) func(*rest.ResponseWriter, *rest.Request) {
	return func(w *rest.ResponseWriter, r *rest.Request) {
		token, id, errt := extractToken(r.PathParam("token"), r.Request)
		if errt != nil {
			log.Println(errt)
			http.Error(w, "Token is required - pass /token/<token>/<path>", http.StatusForbidden)
			return
		}

		if token.D == 0 {
			log.Println("http: Recommend passing 'd' as an argument for the current date")
		}
		if token.U == "" {
			log.Println("http: Recommend passing 'u' as an argument for the associated user")
		}

		job, errh := handler(id, token, w, r)
		if errh != nil {
			if errh != ErrHandledResponse {
				http.Error(w, "Invalid request: "+errh.Error()+"\n", http.StatusBadRequest)
			}
			return
		}

		wait, errd := dispatch.Dispatch(job)
		if errd == jobs.ErrRanToCompletion {
			http.Error(w, errd.Error(), http.StatusNoContent)
			return
		} else if errd != nil {
			serveRequestError(w, apiRequestError{errd, errd.Error(), http.StatusServiceUnavailable})
			return
		}
		<-wait
	}
}

func apiPutContainer(reqid jobs.RequestIdentifier, token *TokenData, w *rest.ResponseWriter, r *rest.Request) (jobs.Job, error) {
	gearId, errg := gears.NewIdentifier(token.ResourceLocator())
	if errg != nil {
		return nil, errg
	}
	if token.ResourceType() == "" {
		return nil, errors.New("A container must have an image identifier")
	}

	data := jobs.ExtendedInstallContainerData{}
	if r.Body != nil {
		dec := json.NewDecoder(limitedBodyReader(r))
		if err := dec.Decode(&data); err != nil && err != io.EOF {
			return nil, err
		}
	}
	if data.Ports == nil {
		data.Ports = make([]gears.PortPair, 0)
	}

	if data.Environment != nil {
		env := data.Environment
		if env.Id == gears.InvalidIdentifier {
			return nil, errors.New("You must specify an environment identifier on creation.")
		}
	}

	return &jobs.InstallContainerJobRequest{
		NewHttpJobResponse(w.ResponseWriter, false),
		jobs.JobRequest{reqid},
		gearId,
		token.U,
		token.ResourceType(),
		&data,
	}, nil
}

func apiListContainers(reqid jobs.RequestIdentifier, token *TokenData, w *rest.ResponseWriter, r *rest.Request) (jobs.Job, error) {
	return &jobs.ListContainersRequest{NewHttpJobResponse(w.ResponseWriter, false), jobs.JobRequest{reqid}}, nil
}

func apiListImages(reqid jobs.RequestIdentifier, token *TokenData, w *rest.ResponseWriter, r *rest.Request) (jobs.Job, error) {
	return &jobs.ListImagesRequest{NewHttpJobResponse(w.ResponseWriter, false), jobs.JobRequest{reqid}}, nil
}

func apiGetContainerLog(reqid jobs.RequestIdentifier, token *TokenData, w *rest.ResponseWriter, r *rest.Request) (jobs.Job, error) {
	gearId, errg := gears.NewIdentifier(token.ResourceLocator())
	if errg != nil {
		return nil, errg
	}
	return &jobs.ContainerLogJobRequest{
		NewHttpJobResponse(w.ResponseWriter, false),
		jobs.JobRequest{reqid},
		gearId,
		token.U,
	}, nil
}

func apiPutKeys(reqid jobs.RequestIdentifier, token *TokenData, w *rest.ResponseWriter, r *rest.Request) (jobs.Job, error) {
	data := jobs.ExtendedCreateKeysData{}
	if r.Body != nil {
		dec := json.NewDecoder(limitedBodyReader(r))
		if err := dec.Decode(&data); err != nil && err != io.EOF {
			return nil, err
		}
	}
	if err := data.Check(); err != nil {
		return nil, err
	}
	return &jobs.CreateKeysJobRequest{
		NewHttpJobResponse(w.ResponseWriter, true),
		jobs.JobRequest{reqid},
		token.U,
		&data,
	}, nil
}

func apiPutRepository(reqid jobs.RequestIdentifier, token *TokenData, w *rest.ResponseWriter, r *rest.Request) (jobs.Job, error) {
	repositoryId, errg := gears.NewIdentifier(token.ResourceLocator())
	if errg != nil {
		return nil, errg
	}
	// TODO: convert token into a safe clone spec and commit hash
	return &jobs.CreateRepositoryJobRequest{
		NewHttpJobResponse(w.ResponseWriter, false),
		jobs.JobRequest{reqid},
		repositoryId,
		token.U,
		"ccoleman/githost",
		token.ResourceType(),
	}, nil
}

func apiPutContainerAction(reqid jobs.RequestIdentifier, token *TokenData, w *rest.ResponseWriter, r *rest.Request) (jobs.Job, error) {
	action := r.PathParam("action")
	gearId, errg := gears.NewIdentifier(token.ResourceLocator())
	if errg != nil {
		return nil, errg
	}
	switch action {
	case "started":
		return &jobs.StartedContainerStateJobRequest{
			NewHttpJobResponse(w.ResponseWriter, false),
			jobs.JobRequest{reqid},
			gearId,
			token.U,
		}, nil
	case "stopped":
		return &jobs.StoppedContainerStateJobRequest{
			NewHttpJobResponse(w.ResponseWriter, false),
			jobs.JobRequest{reqid},
			gearId,
			token.U,
		}, nil
	default:
		return nil, errors.New("You must provide a valid action for this container to take")
	}
}

func apiPutBuildImageAction(reqid jobs.RequestIdentifier, token *TokenData, w *rest.ResponseWriter, r *rest.Request) (jobs.Job, error) {
	if token.ResourceLocator() == "" {
		return nil, errors.New("You must specifiy the application source to build")
	}
	if token.ResourceType() == "" {
		return nil, errors.New("You must specify a base image")
	}

	source := token.ResourceLocator() // token.R
	baseImage := token.ResourceType() // token.T
	tag := token.U

	data := jobs.ExtendedBuildImageData{}
	if r.Body != nil {
		dec := json.NewDecoder(r.Body)
		if err := dec.Decode(&data); err != nil && err != io.EOF {
			return nil, err
		}
	}

	return &jobs.BuildImageJobRequest{
		NewHttpJobResponse(w.ResponseWriter, false),
		jobs.JobRequest{reqid},
		source,
		baseImage,
		tag,
		&data,
	}, nil
}

func apiPutEnvironment(reqid jobs.RequestIdentifier, token *TokenData, w *rest.ResponseWriter, r *rest.Request) (jobs.Job, error) {
	id, errg := gears.NewIdentifier(token.ResourceLocator())
	if errg != nil {
		return nil, errg
	}

	data := jobs.ExtendedEnvironmentData{}
	if r.Body != nil {
		dec := json.NewDecoder(limitedBodyReader(r))
		if err := dec.Decode(&data); err != nil && err != io.EOF {
			return nil, err
		}
	}
	if err := data.Check(); err != nil {
		return nil, err
	}
	data.Id = id

	return &jobs.PutEnvironmentJobRequest{
		NewHttpJobResponse(w.ResponseWriter, false),
		jobs.JobRequest{reqid},
		&data,
	}, nil
}

func apiPatchEnvironment(reqid jobs.RequestIdentifier, token *TokenData, w *rest.ResponseWriter, r *rest.Request) (jobs.Job, error) {
	id, errg := gears.NewIdentifier(token.ResourceLocator())
	if errg != nil {
		return nil, errg
	}

	data := jobs.ExtendedEnvironmentData{}
	if r.Body != nil {
		dec := json.NewDecoder(limitedBodyReader(r))
		if err := dec.Decode(&data); err != nil && err != io.EOF {
			return nil, err
		}
	}
	if err := data.Check(); err != nil {
		return nil, err
	}
	data.Id = id

	return &jobs.PatchEnvironmentJobRequest{
		NewHttpJobResponse(w.ResponseWriter, false),
		jobs.JobRequest{reqid},
		&data,
	}, nil
}

func apiGetContent(reqid jobs.RequestIdentifier, token *TokenData, w *rest.ResponseWriter, r *rest.Request) (jobs.Job, error) {
	if token.ResourceLocator() == "" {
		return nil, errors.New("You must specify the location of the content you want to access")
	}
	if token.ResourceType() == "" {
		return nil, errors.New("You must specify the type of the content you want to access")
	}

	return &jobs.ContentJobRequest{
		NewHttpJobResponse(w.ResponseWriter, false),
		jobs.JobRequest{reqid},
		token.ResourceType(),
		token.ResourceLocator(),
		r.PathParam("*"),
	}, nil
}

func apiLinkContainers(reqid jobs.RequestIdentifier, token *TokenData, w *rest.ResponseWriter, r *rest.Request) (jobs.Job, error) {
	id, errg := gears.NewIdentifier(token.ResourceLocator())
	if errg != nil {
		return nil, errg
	}

	data := jobs.ExtendedLinkContainersData{}
	if r.Body != nil {
		dec := json.NewDecoder(limitedBodyReader(r))
		if err := dec.Decode(&data); err != nil && err != io.EOF {
			return nil, err
		}
	}

	return &jobs.LinkContainersJobRequest{
		NewHttpJobResponse(w.ResponseWriter, false),
		jobs.JobRequest{reqid},
		id,
		&data,
	}, nil
}

func limitedBodyReader(r *rest.Request) io.Reader {
	return io.LimitReader(r.Body, 100*1024)
}

func extractToken(segment string, r *http.Request) (token *TokenData, id jobs.RequestIdentifier, rerr *apiRequestError) {
	if segment == "__test__" {
		t, err := NewTokenFromMap(r.URL.Query())
		if err != nil {
			rerr = &apiRequestError{err, "Invalid test query: " + err.Error(), http.StatusForbidden}
			return
		}
		token = t
	} else {
		t, err := NewTokenFromString(segment)
		if err != nil {
			rerr = &apiRequestError{err, "Invalid authorization token", http.StatusForbidden}
			return
		}
		token = t
	}

	if token.I == "" {
		i := make(jobs.RequestIdentifier, 16)
		_, errr := rand.Read(i)
		if errr != nil {
			rerr = &apiRequestError{errr, "Unable to generate token for this request: " + errr.Error(), http.StatusBadRequest}
			return
		}
		id = i
	} else {
		i, errr := token.RequestId()
		if errr != nil {
			rerr = &apiRequestError{errr, "Unable to parse token for this request: " + errr.Error(), http.StatusBadRequest}
			return
		}
		id = i
	}

	return
}

type apiRequestError struct {
	Error   error
	Message string
	Status  int
}

func serveRequestError(w http.ResponseWriter, err apiRequestError) {
	log.Print(err.Message, err.Error)
	http.Error(w, err.Message, err.Status)
}