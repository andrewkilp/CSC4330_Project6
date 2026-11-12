module github.com/andrewkilp/CSC4330_Project6/apps/api

go 1.26.4

require github.com/andrewkilp/CSC4330_Project6/packages/go/handlers v0.0.0

require (
	github.com/andrewkilp/CSC4330_Project6/packages/go/claude v0.0.0 // indirect
	github.com/andrewkilp/CSC4330_Project6/packages/go/conversation v0.0.0 // indirect
	github.com/andrewkilp/CSC4330_Project6/packages/go/mock v0.0.0
)

replace github.com/andrewkilp/CSC4330_Project6/packages/go/handlers => ../../packages/go/handlers

replace github.com/andrewkilp/CSC4330_Project6/packages/go/mock => ../../packages/go/mock

replace github.com/andrewkilp/CSC4330_Project6/packages/go/conversation => ../../packages/go/conversation

replace github.com/andrewkilp/CSC4330_Project6/packages/go/claude => ../../packages/go/claude
