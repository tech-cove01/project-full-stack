# CAP-08 早期派生JSON字节补正

最终Git字节审计发现仅 `failure-window.json` 一项不匹配：它在08A提交时早于CAP-08的 `-text` 属性，原36147字节CRLF被Git规范为35122字节LF。属性补齐后，常规add仍沿用未改变的已跟踪文件索引，未重新收录该文件的原件字节。

本地原件始终未改，SHA256为 `322f3e22bf1cd95da36dffa92ade37ddf1c33c97af4ac136d57f2fac9c10e774`，已在冻结raw-manifest中记录。旧blob为 `607f784cf7816fc8022c2c0478e75a8280cdf65da308754d5f490111f87a278a`。二者仅CRLF/LF不同，JSON语义相同；原生诊断/正式负载工件均不受影响，旧CAP-06/07文件未修改。

只对该明确路径用现有 `-text` 属性重新暂存原件字节并追加提交，不修改文件内容、不amend历史、不更新原manifest或任何测量hash。随后对全部131份raw及manifest本身重新作Git blob/原件/hash核对；实际结论见git-byte-audit.json。
