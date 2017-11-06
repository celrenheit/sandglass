package broker

import (
	"bytes"
	"context"
	"fmt"

	"github.com/celrenheit/sandflake"

	"github.com/celrenheit/sandglass/sgproto"
	"github.com/celrenheit/sandglass/topic"
)

func (b *Broker) Acknowledge(ctx context.Context, topicName, partitionName, consumerGroup, consumerName string, offset sandflake.ID) (bool, error) {
	return b.mark(ctx, topicName, partitionName, consumerGroup, consumerName, offset, sgproto.LastOffsetRequest_Acknowledged)
}

func (b *Broker) AcknowledgeMessages(ctx context.Context, topicName, partitionName, consumerGroup, consumerName string, offsets []sandflake.ID) error {
	topic := b.getTopic(ConsumerOffsetTopicName)
	p := topic.ChoosePartitionForKey(partitionKey(topicName, partitionName, consumerGroup, consumerName))

	n := b.getPartitionLeader(ConsumerOffsetTopicName, p.Id)
	if n == nil {
		return ErrNoLeaderFound
	}

	if n.Name != b.Name() {
		change := &sgproto.MultiOffsetChangeRequest{
			Topic:         topicName,
			Partition:     partitionName,
			ConsumerGroup: consumerGroup,
			ConsumerName:  consumerName,
			Offsets:       offsets,
		}

		_, err := n.AcknowledgeMessages(ctx, change)

		if err != nil {
			return err
		}

		return nil
	}

	msgs := []*sgproto.Message{}
	for _, offset := range offsets {
		msgs = append(msgs, &sgproto.Message{
			Topic:         ConsumerOffsetTopicName,
			Partition:     p.Id,
			Offset:        offset,
			Key:           partitionKey(topicName, partitionName, consumerGroup, consumerName),
			ClusteringKey: generateClusterKey(offset, sgproto.LastOffsetRequest_Acknowledged),
			Value:         []byte{byte(sgproto.LastOffsetRequest_Acknowledged)},
		})
	}

	return b.PublishMessages(ctx, msgs)
}

func (b *Broker) Commit(ctx context.Context, topicName, partitionName, consumerGroup, consumerName string, offset sandflake.ID) (bool, error) {
	return b.mark(ctx, topicName, partitionName, consumerGroup, consumerName, offset, sgproto.LastOffsetRequest_Commited)
}

func (b *Broker) mark(ctx context.Context, topicName, partitionName, consumerGroup, consumerName string, offset sandflake.ID, kind sgproto.LastOffsetRequest_Kind) (bool, error) {
	topic := b.getTopic(ConsumerOffsetTopicName)
	p := topic.ChoosePartitionForKey(partitionKey(topicName, partitionName, consumerGroup, consumerName))

	n := b.getPartitionLeader(ConsumerOffsetTopicName, p.Id)
	if n == nil {
		return false, ErrNoLeaderFound
	}

	if n.Name != b.Name() {
		change := &sgproto.OffsetChangeRequest{
			Topic:         topicName,
			Partition:     partitionName,
			ConsumerGroup: consumerGroup,
			ConsumerName:  consumerName,
			Offset:        offset,
		}

		var (
			res *sgproto.OffsetChangeReply
			err error
		)
		switch kind {
		case sgproto.LastOffsetRequest_Acknowledged:
			res, err = n.Acknowledge(ctx, change)
		case sgproto.LastOffsetRequest_Commited:
			res, err = n.Commit(ctx, change)
		}
		if err != nil {
			return false, err
		}

		return res.Success, nil
	}

	res, err := b.PublishMessage(ctx, &sgproto.Message{
		Topic:         ConsumerOffsetTopicName,
		Partition:     p.Id,
		Offset:        offset,
		Key:           partitionKey(topicName, partitionName, consumerGroup, consumerName),
		ClusteringKey: generateClusterKey(offset, kind),
		Value:         []byte{byte(kind)},
	})
	return res != nil, err
}

func (b *Broker) LastOffset(ctx context.Context, topicName, partitionName, consumerGroup, consumerName string, kind sgproto.LastOffsetRequest_Kind) (sandflake.ID, error) {
	topic := b.getTopic(ConsumerOffsetTopicName)
	pk := partitionKey(topicName, partitionName, consumerGroup, consumerName)
	p := topic.ChoosePartitionForKey(pk)

	n := b.getPartitionLeader(ConsumerOffsetTopicName, p.Id)
	if n == nil {
		return sandflake.Nil, ErrNoLeaderFound
	}

	if n.Name != b.Name() {
		res, err := n.LastOffset(ctx, &sgproto.LastOffsetRequest{
			Topic:         topicName,
			Partition:     partitionName,
			ConsumerGroup: consumerGroup,
			ConsumerName:  consumerName,
			Kind:          kind,
		})
		if err != nil {
			return sandflake.Nil, err
		}

		return res.Offset, nil
	}

	lastKind := byte(kind)

	return b.last(p, pk, lastKind)
}

func (b *Broker) last(p *topic.Partition, pk []byte, kind byte) (sandflake.ID, error) {
	msg, err := p.GetMessage(sandflake.Nil, pk, []byte{kind})
	if err != nil {
		return sandflake.Nil, err
	}

	if msg == nil {
		return sandflake.Nil, nil
	}

	if len(msg.Value) != 1 {
		return sandflake.Nil, fmt.Errorf("LastCommitedOffset malformed value '%v'", msg.Value)
	}

	return msg.Offset, nil
}

func (b *Broker) isAcknoweldged(ctx context.Context, topicName, partition, consumerGroup string, offset sandflake.ID) (bool, error) {
	topic := b.getTopic(ConsumerOffsetTopicName)
	if topic == nil {
		return false, ErrTopicNotFound
	}
	pk := partitionKey(topicName, partition, consumerGroup, "NOT SET")
	p := topic.ChoosePartitionForKey(pk)
	clusterKey := generateClusterKey(offset, sgproto.LastOffsetRequest_Acknowledged)
	return b.hasKeyInPartition(ctx, ConsumerOffsetTopicName, p, pk, clusterKey)
}

func partitionKey(topicName, partitionName, consumerGroup, consumerName string) []byte {
	return bytes.Join([][]byte{
		[]byte("offsets"),
		[]byte(topicName),
		[]byte(partitionName),
		[]byte(consumerGroup),
		// []byte(consumerName),
	}, []byte{'/'})
}

func generateClusterKey(offset sandflake.ID, kind sgproto.LastOffsetRequest_Kind) []byte {
	return bytes.Join([][]byte{
		[]byte(offset.String()), // .String() for debugging, remove this later
		[]byte{byte(kind)},
	}, []byte{'/'})
}
