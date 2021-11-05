/*
Copyright 2021 Teodor Spæren

   Licensed under the Apache License, Version 2.0 (the "License");
   you may not use this file except in compliance with the License.
   You may obtain a copy of the License at

       http://www.apache.org/licenses/LICENSE-2.0

   Unless required by applicable law or agreed to in writing, software
   distributed under the License is distributed on an "AS IS" BASIS,
   WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
   See the License for the specific language governing permissions and
   limitations under the License.
*/

package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"runtime/trace"
	"strings"
	"sync"
	"time"

	"github.com/hako/durafmt"
	"github.com/xanzy/go-gitlab"
)

type EnvDep struct {
	Name string
	Prod int
}

var (
	projectFlag = flag.String("project", "", "selects the project to be used")
	traceFlag   = flag.String("trace", "", "file to write trace to")
)

func main() {
	flag.Parse()
	if *projectFlag == "" {
		log.Fatal("project not set")
	}

	if *traceFlag != "" {
		f, err := os.Create(*traceFlag)
		if err != nil {
			log.Fatalf("couldn't open trace file: %v", err)
		}
		defer f.Close()

		if err := trace.Start(f); err != nil {
			log.Fatalf("couldn't start trace: %v", err)
		}
		defer trace.Stop()
	}

	ctx := context.Background()

	ctx, tsk := trace.NewTask(ctx, "full-run")
	defer tsk.End()

	token, ok := os.LookupEnv("GITLAB_TOKEN")
	if !ok {
		log.Fatal("token not set\n")
	}

	c, err := gitlab.NewClient(token)
	if err != nil {
		log.Fatalf("creating client: %v", err)
	}

	pid, err := getProjectID(ctx, c)
	if err != nil {
		log.Fatalf("get project id: %v", err)
	}

	envDeps, err := getEnvs(ctx, c, pid)
	if err != nil {
		log.Fatalf("get envs: %v", err)
	}

	if err := getDrifts(ctx, c, pid, envDeps); err != nil {
		log.Fatalf("get drifts: %v", err)
	}
}

func getDrifts(ctx context.Context, c *gitlab.Client, pid int, envDeps []EnvDep) error {
	ctx, tsk := trace.NewTask(ctx, "get-drifts")
	defer tsk.End()

	var wg sync.WaitGroup

	fmt.Printf("SERVICE           | SHORT SHA | LAST DEPLOY\n")
	for _, envDep := range envDeps {
		wg.Add(1)
		go func(ed EnvDep) {
			defer wg.Done()

			if err := getDrift(ctx, c, pid, ed); err != nil {
				log.Printf("get drift %s: %v", ed.Name, err)
			}
		}(envDep)
	}

	wg.Wait()
	return nil
}

func getDrift(ctx context.Context, c *gitlab.Client, pid int, ed EnvDep) error {
	ctx, tsk := trace.NewTask(ctx, "get-drift")
	defer tsk.End()

	trace.Log(ctx, "service", ed.Name)
	penv, r, err := c.Environments.GetEnvironment(pid, ed.Prod, gitlab.WithContext(ctx))
	if err != nil {
		return fmt.Errorf("get prod environment: %v", err)
	}
	defer r.Body.Close()

	if penv.LastDeployment == nil {
		return nil
	}

	pdep := penv.LastDeployment.Deployable

	lastDep := time.Since(*pdep.FinishedAt)
	dd := durafmt.Parse(lastDep).LimitFirstN(2)
	fmt.Printf("%-18s| %s  | %s\n", ed.Name, pdep.Commit.ShortID, dd.String())

	return nil
}

func getEnvs(ctx context.Context, c *gitlab.Client, pid int) ([]EnvDep, error) {
	ctx, tsk := trace.NewTask(ctx, "get-envs")
	defer tsk.End()

	page := 1
	perPage := 20

	allEnvs := make([]*gitlab.Environment, 0)

	for page != 0 {
		envs, r, err := c.Environments.ListEnvironments(pid, &gitlab.ListEnvironmentsOptions{
			ListOptions: gitlab.ListOptions{
				Page:    page,
				PerPage: perPage,
			},
			States: gitlab.String("available"),
			Search: gitlab.String("prod/"),
		}, gitlab.WithContext(ctx))
		if err != nil {
			return nil, fmt.Errorf("list environments: %v", err)
		}
		defer r.Body.Close()

		allEnvs = append(allEnvs, envs...)

		page = r.NextPage
	}

	servDeps := make([]EnvDep, 0)
	for _, env := range allEnvs {
		parts := strings.Split(env.Name, "/")
		if len(parts) != 2 {
			continue
		}
		servDeps = append(servDeps, EnvDep{
			Name: parts[1],
			Prod: env.ID,
		})
	}

	return servDeps, nil
}

func getProjectID(ctx context.Context, c *gitlab.Client) (int, error) {
	ctx, tsk := trace.NewTask(ctx, "get-project-id")
	defer tsk.End()

	ps, r, err := c.Projects.ListProjects(&gitlab.ListProjectsOptions{
		SearchNamespaces: gitlab.Bool(true),
		Search:           gitlab.String(*projectFlag),
		Visibility:       gitlab.Visibility(gitlab.PrivateVisibility),
	}, gitlab.WithContext(ctx))
	if err != nil {
		return 0, fmt.Errorf("listing projects: %v", err)
	}
	defer r.Body.Close()

	if len(ps) > 1 {
		return 0, fmt.Errorf("too many projects matched")
	}
	if len(ps) < 1 {
		return 0, fmt.Errorf("no projects matched")
	}

	p := ps[0]
	return p.ID, nil
}
