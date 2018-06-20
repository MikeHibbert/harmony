package node

import (
	"bytes"
	"harmony-benchmark/blockchain"
	"time"
	"net"
	"harmony-benchmark/p2p"
	"harmony-benchmark/common"
	"os"
	"encoding/gob"
)

// Handler of the leader node.
func (node *Node) NodeHandler(conn net.Conn) {
	defer conn.Close()

	// Read p2p message payload
	content, err := p2p.ReadMessageContent(conn)

	consensus := node.consensus
	if err != nil {
		node.log.Error("Read p2p data failed", "err", err, "node", node)
		return
	}

	msgCategory, err := common.GetMessageCategory(content)
	if err != nil {
		node.log.Error("Read node type failed", "err", err, "node", node)
		return
	}

	msgType, err := common.GetMessageType(content)
	if err != nil {
		node.log.Error("Read action type failed", "err", err, "node", node)
		return
	}

	msgPayload, err := common.GetMessagePayload(content)
	if err != nil {
		node.log.Error("Read message payload failed", "err", err, "node", node)
		return
	}

	switch msgCategory {
	case common.COMMITTEE:
		actionType := common.CommitteeMessageType(msgType)
		switch actionType {
		case common.CONSENSUS:
			if consensus.IsLeader {
				consensus.ProcessMessageLeader(msgPayload)
			} else {
				consensus.ProcessMessageValidator(msgPayload)
			}
		}
	case common.NODE:
		actionType := common.NodeMessageType(msgType)
		switch actionType {
		case common.TRANSACTION:
			node.transactionMessageHandler(msgPayload)
		case common.CONTROL:
			controlType := msgPayload[0]
			if ControlMessageType(controlType) == STOP {
				node.log.Debug("Stopping Node", "node", node)
				os.Exit(0)
			}

		}
	}
}

func (node *Node) transactionMessageHandler(msgPayload []byte) {
	txMessageType := TransactionMessageType(msgPayload[0])

	switch txMessageType {
	case SEND:
		txDecoder := gob.NewDecoder(bytes.NewReader(msgPayload[1:])) // skip the SEND messge type

		txList := new([]*blockchain.Transaction)
		err := txDecoder.Decode(&txList)
		if err != nil {
			node.log.Error("Failed deserializing transaction list", "node", node)
		}
		node.addPendingTransactions(*txList)
	case REQUEST:
		reader := bytes.NewBuffer(msgPayload[1:])
		var txIds map[[32]byte]bool
		buf := make([]byte, 32) // 32 byte hash Id
		for {
			_, err := reader.Read(buf)
			if err != nil {
				break
			}

			var txId [32]byte
			copy(txId[:], buf)
			txIds[txId] = true
		}

		var txToReturn []*blockchain.Transaction
		for _, tx := range node.pendingTransactions {
			if txIds[tx.ID] {
				txToReturn = append(txToReturn, tx)
			}
		}

		// TODO: return the transaction list to requester
	}
}


func (node *Node) WaitForConsensusReady(readySignal chan int) {
	node.log.Debug("Waiting for consensus ready", "node", node)

	var newBlock *blockchain.Block
	for { // keep waiting for consensus ready
		<-readySignal
		//node.log.Debug("Adding new block", "currentChainSize", len(node.blockchain.Blocks), "numTxs", len(node.blockchain.GetLatestBlock().Transactions), "PrevHash", node.blockchain.GetLatestBlock().PrevBlockHash, "Hash", node.blockchain.GetLatestBlock().Hash)
		if newBlock != nil {
			// Consensus is done on the newBlock (in the previous round of consensus), add it to blockchain
			node.blockchain.Blocks = append(node.blockchain.Blocks, newBlock)
			// Update UTXO pool
			node.UtxoPool.Update(node.transactionInConsensus)
			// Clear transaction-in-consensus list
			node.transactionInConsensus = []*blockchain.Transaction{}
		}
		for {
			// Once we have more than 10 transactions pending we will try creating a new block
			if len(node.pendingTransactions) >= 10 {
				selectedTxs := node.getTransactionsForNewBlock()

				if len(selectedTxs) == 0 {
					node.log.Debug("No valid transactions exist", "pendingTx", len(node.pendingTransactions))
				} else {
					node.log.Debug("Creating new block", "numTxs", len(selectedTxs), "pendingTxs", len(node.pendingTransactions), "currentChainSize", len(node.blockchain.Blocks))

					node.transactionInConsensus = selectedTxs
					newBlock = blockchain.NewBlock(selectedTxs, node.blockchain.GetLatestBlock().Hash)
					break
				}
			}
			// If not enough transactions to run consensus,
			// periodically check whether we have enough transactions to package into block.
			time.Sleep(1 * time.Second)
		}

		// Send the new block to consensus so it can be confirmed.
		node.BlockChannel <- *newBlock
	}
}

func (node *Node) VerifyNewBlock(block *blockchain.Block) bool {
	return node.UtxoPool.VerifyTransactions(block.Transactions)
}