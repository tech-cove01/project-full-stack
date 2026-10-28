# GO-075 索引兼容性守护

独立问题：旧查询先调用查询embedding，再只选匹配model/dimension的chunk，切模型后会成功返回空集；部分错误向量被静默跳过。首次真实MySQL/Redis回归复现：[index-initial.txt](index-initial.txt)，`rejected=false calls=1`。

现在使用Provider endpoint+embedding model+dimension的固定指纹保存向量空间身份，实际展示模型名保持原值。切换Provider/模型/维度后旧索引明确不可用，须管理者显式重建；更换Key或Chat模型不强制重建。不改SQL结构，已有stub模型名保持兼容；旧真实向量若仅有模型名须重建一次，不自动付费重建。

选定公开范围中，校验不可变版本及完整chunk数量/ordinal/内容、向量配置/长度/有限非零值后再调用查询embedding。缺失或不兼容返回`ErrUnsafe`，不伪装成无规则拒答。草稿、停用及其它门店不参与该范围。

第一次实现通过6个子项：[index-final.txt](index-final.txt)。独立审查又发现慢embedding期间停用后匿名查询仍可能返回旧内容，以及只检查全空漏掉多分片部分缺失；提交前已加embedding前后Snapshot、最终Validate和不可变正文分片核对。受控匿名HTTP停用与8子项最终通过：[index-review-final3.txt](index-review-final3.txt)。这是每次请求的观测校验，不承诺响应发送与后续任意撤权线性串行。

保留两次扩充夹具失败：[index-review-final.txt](index-review-final.txt)错误要求全部chunk都达到相关性阈值；[index-review-final2.txt](index-review-final2.txt)重复段落被原chunker正常去重而未形成多分片。改为两个不同400字段落，明确先核验多分片夹具，再分别验证相关性与完整性；未为通过测试更改chunk算法。

命令（backend目录、专用MySQL3331/Redis6403、GO_INTEGRATION=1）：`go test -count=1 -run '^TestIntegrationIndexCompatibility$' -v ./internal/aiknowledge`。全部8子项通过，不调用付费模型。

提交前独立复核：provider_adapter确认此前两发现已关闭，无新增可行动问题；独立重跑`go test -count=1 ./internal/aiknowledge ./internal/ai`通过。普通全套Go测试和vet也通过于此票当前实现工作树，最终票级结果另列。
