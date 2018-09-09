package executor

import (
	"bytes"

	"gitlab.33.cn/chain33/chain33/account"
	"gitlab.33.cn/chain33/chain33/client"
	dbm "gitlab.33.cn/chain33/chain33/common/db"
	"gitlab.33.cn/chain33/chain33/plugin/dapp/evm/executor/vm/common"
	"gitlab.33.cn/chain33/chain33/types"
	pt "gitlab.33.cn/chain33/chain33/types/executor/paracross"
)

type action struct {
	coinsAccount *account.DB
	db           dbm.KV
	localdb      dbm.KVDB
	txhash       []byte
	fromaddr     string
	blocktime    int64
	height       int64
	execaddr     string
	api          client.QueueProtocolAPI
}

func newAction(t *Paracross, tx *types.Transaction) *action {
	hash := tx.Hash()
	fromaddr := tx.From()
	return &action{t.GetCoinsAccount(), t.GetStateDB(), t.GetLocalDB(), hash, fromaddr,
		t.GetBlockTime(), t.GetHeight(), t.GetAddr(), t.GetApi()}
}

func getNodes(db dbm.KV, title string) ([]string, error) {
	key := calcConfigNodesKey(title)
	item, err := db.Get(key)
	if err != nil {
		clog.Info("getNodes", "get db key", key, "failed", err)
		if isNotFound(err) {
			return nil, types.ErrTitleNotExist
		}
		return nil, err
	}
	var config types.ConfigItem
	err = types.Decode(item, &config)
	if err != nil {
		return nil, err
	}
	value := config.GetArr()
	if value == nil {
		// 在配置地址后，发现配置错了， 删除会出现这种情况
		return []string{}, nil
	}
	return value.Value, nil
}

func validTitle(title string) bool {
	return len(title) > 0
}

func validNode(addr string, nodes []string) bool {
	for _, n := range nodes {
		if n == addr {
			return true
		}
	}
	return false
}

func checkCommitInfo(commit *types.ParacrossCommitAction) error {
	if commit.Status == nil {
		return types.ErrInputPara
	}
	if commit.Status.Height == 0 {
		if len(commit.Status.Title) == 0 || len(commit.Status.BlockHash) == 0 {
			return types.ErrInputPara
		}
		return nil
	}
	if len(commit.Status.MainBlockHash) == 0 || len(commit.Status.Title) == 0 || commit.Status.Height < 0 ||
		len(commit.Status.PreBlockHash) == 0 || len(commit.Status.BlockHash) == 0 ||
		len(commit.Status.StateHash) == 0 || len(commit.Status.PreStateHash) == 0 {
		return types.ErrInputPara
	}

	return nil
}

func isCommitDone(f interface{}, nodes []string, mostSameHash int) bool {
	return float32(mostSameHash) > float32(len(nodes))*float32(2)/float32(3)
}

func makeCommitReceipt(addr string, commit *types.ParacrossCommitAction, prev, current *types.ParacrossHeightStatus) *types.Receipt {
	key := calcTitleHeightKey(commit.Status.Title, commit.Status.Height)
	log := &types.ReceiptParacrossCommit{
		Addr:    addr,
		Status:  commit.Status,
		Prev:    prev,
		Current: current,
	}
	return &types.Receipt{
		Ty: types.ExecOk,
		KV: []*types.KeyValue{
			{key, types.Encode(current)},
		},
		Logs: []*types.ReceiptLog{
			{
				Ty:  types.TyLogParacrossCommit,
				Log: types.Encode(log),
			},
		},
	}
}

func makeRecordReceipt(addr string, commit *types.ParacrossCommitAction) *types.Receipt {
	log := &types.ReceiptParacrossRecord{
		Addr:   addr,
		Status: commit.Status,
	}
	return &types.Receipt{
		Ty: types.ExecOk,
		KV: nil,
		Logs: []*types.ReceiptLog{
			{
				Ty:  types.TyLogParacrossRecord,
				Log: types.Encode(log),
			},
		},
	}
}

func makeDoneReceipt(addr string, commit *types.ParacrossCommitAction, current *types.ParacrossHeightStatus,
	most, commitCount, totalCount int32) *types.Receipt {

	log := &types.ReceiptParacrossDone{
		TotalNodes:     totalCount,
		TotalCommit:    commitCount,
		MostSameCommit: most,
		Title:          commit.Status.Title,
		Height:         commit.Status.Height,
		StateHash:      commit.Status.StateHash,
		TxCounts:       commit.Status.TxCounts,
		TxResult:       commit.Status.TxResult,
	}
	key := calcTitleKey(commit.Status.Title)
	stat := &types.ParacrossStatus{
		Title:     commit.Status.Title,
		Height:    commit.Status.Height,
		BlockHash: commit.Status.BlockHash,
	}
	return &types.Receipt{
		Ty: types.ExecOk,
		KV: []*types.KeyValue{
			{key, types.Encode(stat)},
		},
		Logs: []*types.ReceiptLog{
			{
				Ty:  types.TyLogParacrossDone,
				Log: types.Encode(log),
			},
		},
	}
}

func getMostCommit(stat *types.ParacrossHeightStatus) (int, string) {
	stats := make(map[string]int)
	n := len(stat.Details.Addrs)
	for i := 0; i < n; i++ {
		if _, ok := stats[string(stat.Details.BlockHash[i])]; ok {
			stats[string(stat.Details.BlockHash[i])]++
		} else {
			stats[string(stat.Details.BlockHash[i])] = 1
		}
	}
	most := -1
	var hash string
	for k, v := range stats {
		if v > most {
			most = v
			hash = k
		}
	}
	return most, hash
}

func hasCommited(addrs []string, addr string) (bool, int) {
	for i, a := range addrs {
		if a == addr {
			return true, i
		}
	}
	return false, 0
}

func (a *action) Commit(commit *types.ParacrossCommitAction) (*types.Receipt, error) {
	err := checkCommitInfo(commit)
	if err != nil {
		return nil, err
	}
	clog.Debug("paracross.Commit check", "input", commit.Status)
	if !validTitle(commit.Status.Title) {
		return nil, types.ErrInvalidTitle
	}

	nodes, err := getNodes(a.db, commit.Status.Title)
	if err != nil {
		clog.Error("paracross.Commit", "getNodes", err)
		return nil, err
	}

	if !validNode(a.fromaddr, nodes) {
		clog.Error("paracross.Commit", "validNode", a.fromaddr)
		return nil, types.ErrNodeNotForTheTitle
	}

	titleStatus, err := getTitle(a.db, calcTitleKey(commit.Status.Title))
	if err != nil {
		clog.Error("paracross.Commit", "getTitle", a.fromaddr)
		return nil, err
	}

	if titleStatus.Height+1 == commit.Status.Height && commit.Status.Height > 0 {
		if !bytes.Equal(titleStatus.BlockHash, commit.Status.PreBlockHash) {
			clog.Error("paracross.Commit", "check PreBlockHash", common.Bytes2Hex(titleStatus.BlockHash),
				"commit tx", common.Bytes2Hex(commit.Status.PreBlockHash), "commitheit", commit.Status.Height,
				"from", a.fromaddr)
			return nil, types.ErrParaBlockHashNoMatch
		}
	}

	// 极端情况， 分叉后在平行链生成了 commit交易 并发送之后， 但主链完成回滚，时间序如下
	// 主链   （1）Bn1        （3） rollback-Bn1   （4） commit-done in Bn2
	// 平行链         （2）commit                                 （5） 将得到一个错误的块
	// 所以有必要做这个检测
	blockHash, err := getBlockHash(a.api, commit.Status.MainBlockHeight)
	if err != nil {
		clog.Error("paracross.Commit getBlockHash", "err", err,
			"commit tx Main.height", commit.Status.MainBlockHeight)
		return nil, err
	}
	if !bytes.Equal(blockHash.Hash, commit.Status.MainBlockHash) && commit.Status.Height > 0 {
		clog.Error("paracross.Commit blockHash not match", "db", common.Bytes2Hex(blockHash.Hash),
			"commit tx", common.Bytes2Hex(commit.Status.MainBlockHash), "commitheit", commit.Status.Height,
			"from", a.fromaddr)
		return nil, types.ErrBlockHashNoMatch
	}

	clog.Debug("paracross.Commit check input done")
	// 在完成共识之后来的， 增加 record log， 只记录不修改已经达成的共识
	if commit.Status.Height <= titleStatus.Height {
		clog.Info("paracross.Commit record", "node", a.fromaddr, "titile", commit.Status.Title,
			"height", commit.Status.Height)
		return makeRecordReceipt(a.fromaddr, commit), nil
	}

	// 未共识处理， 接受当前高度以及后续高度
	stat, err := getTitleHeight(a.db, calcTitleHeightKey(commit.Status.Title, commit.Status.Height))
	if err != nil && !isNotFound(err) {
		clog.Error("paracross.Commit getTitleHeight failed", "err", err)
		return nil, err
	}

	var receipt *types.Receipt
	if isNotFound(err) {
		stat = &types.ParacrossHeightStatus{
			Status: pt.ParacrossStatusCommiting,
			Title:  commit.Status.Title,
			Height: commit.Status.Height,
			Details: &types.ParacrossStatusDetails{
				Addrs:     []string{a.fromaddr},
				BlockHash: [][]byte{commit.Status.BlockHash},
			},
		}
		receipt = makeCommitReceipt(a.fromaddr, commit, nil, stat)
	} else {
		copyStat := *stat
		// 如有分叉， 同一个节点可能再次提交commit交易
		found, index := hasCommited(stat.Details.Addrs, a.fromaddr)
		if found {
			stat.Details.BlockHash[index] = commit.Status.BlockHash
		} else {
			stat.Details.Addrs = append(stat.Details.Addrs, a.fromaddr)
			stat.Details.BlockHash = append(stat.Details.BlockHash, commit.Status.BlockHash)
		}
		receipt = makeCommitReceipt(a.fromaddr, commit, &copyStat, stat)
	}
	clog.Info("paracross.Commit commit", "stat", stat)

	if commit.Status.Height > titleStatus.Height+1 {
		saveTitleHeight(a.db, calcTitleHeightKey(commit.Status.Title, commit.Status.Height), stat)
		return receipt, nil
	}

	commitCount := len(stat.Details.Addrs)
	most, _ := getMostCommit(stat)
	if !isCommitDone(stat, nodes, most) {
		saveTitleHeight(a.db, calcTitleHeightKey(commit.Status.Title, commit.Status.Height), stat)
		return receipt, nil
	}

	stat.Status = pt.ParacrossStatusCommitDone
	receiptDone := makeDoneReceipt(a.fromaddr, commit, stat, int32(most), int32(commitCount), int32(len(nodes)))
	receipt.KV = append(receipt.KV, receiptDone.KV...)
	receipt.Logs = append(receipt.Logs, receiptDone.Logs...)
	saveTitleHeight(a.db, calcTitleHeightKey(commit.Status.Title, commit.Status.Height), stat)

	titleStatus.Title = commit.Status.Title
	titleStatus.Height = commit.Status.Height
	titleStatus.BlockHash = commit.Status.BlockHash
	saveTitle(a.db, calcTitleKey(commit.Status.Title), titleStatus)
	clog.Info("paracross.Commit commit", "commitDone", titleStatus)

	if commit.Status.Height > 0 {
		// 联调发现平行链创世区块的交易没有发送过来
		// TODO 触发交易组跨链交易
		print(blockHash)
		// TODO 需要生成本地db 用原交易组查询执行结果
	}
	return receipt, nil
}