package communityprojection

// Message preserves the Kafka source needed to apply or durably park an event.
// It deliberately does not import the Kafka client into projection storage.
type Message struct {
	Topic     string
	Partition int
	Offset    int64
	Key       []byte
	Body      []byte
	Headers   []Header
}

type Header struct {
	Key   string
	Value []byte
}
