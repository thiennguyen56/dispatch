Project structure

dispatch/
├── cmd/
│ └── dispatch/
│ └── main.go # Construct dependencies, start API/workers
├── internal/
│ ├── domain/
│ │ ├── delivery.go # Delivery entity, statuses, valid transitions
│ │ └── attempt.go # Result of a delivery attempt
│ ├── application/
│ │ ├── ports.go # Repository and Sender interfaces
│ │ ├── submit.go # Create and persist a delivery
│ │ ├── get.go # Retrieve a delivery
│ │ ├── process.go # Execute delivery and coordinate state changes
│ │ ├── retry.go # Retry eligibility, limits, backoff policy
│ │ └── process_test.go
│ └── adapters/
│ ├── inbound/
│ │ ├── httpapi/
│ │ │ ├── handler.go
│ │ │ └── routes.go
│ │ └── worker/
│ │ └── pool.go # Poll and invoke processing use case
│ └── outbound/
│ ├── sqlite/
│ │ ├── repository.go
│ │ └── migrations/
│ │ └── 001_init.sql
│ └── webhook/
│ └── sender.go # Make outbound HTTP requests
├── go.mod
└── README.md
