module github.com/andrewkilp/CSC4330_Project6/packages/go/mock

go 1.26.4

require (
	github.com/andrewkilp/CSC4330_Project6/packages/go/claude v0.0.0
	github.com/andrewkilp/CSC4330_Project6/packages/go/conversation v0.0.0
	github.com/andrewkilp/CSC4330_Project6/packages/go/providers v0.0.0
)

replace (
	github.com/andrewkilp/CSC4330_Project6/packages/go/claude => ../claude
	github.com/andrewkilp/CSC4330_Project6/packages/go/conversation => ../conversation
	github.com/andrewkilp/CSC4330_Project6/packages/go/providers => ../providers
)
