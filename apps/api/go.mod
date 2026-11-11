module github.com/andrewkilp/CSC4330_Project6/apps/api

go 1.26.4

require (
	github.com/andrewkilp/CSC4330_Project6/packages/go/auth v0.0.0
	github.com/andrewkilp/CSC4330_Project6/packages/go/handlers v0.0.0
)

replace github.com/andrewkilp/CSC4330_Project6/packages/go/auth => ../../packages/go/auth

replace github.com/andrewkilp/CSC4330_Project6/packages/go/handlers => ../../packages/go/handlers
