# time-since-deploy

Program to show how long it was since a deploy was made to prod in Gitlab.

Takes in a personal token with `read_api` permissions from the `GITLAB_TOKEN`
environment variable. Set the project you want to look at with the 
`-project` commandline flag.

