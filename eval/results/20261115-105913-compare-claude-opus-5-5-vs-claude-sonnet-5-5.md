# Model comparison: claude-opus-5-5 and claude-sonnet-5-5

Same golden set, one run per brief and model (2026-11-15 10:58 UTC and 2026-11-15 10:59 UTC). One run each is smoke level: a difference here is what these
answers were, not a measured rate.

| | claude-opus-5-5 | claude-sonnet-5-5 |
|---|---|---|
| briefs passed | 1 of 1 | 1 of 1 |
| cost | $0.0353 | $0.0153 |
| tokens in / out | 4855 / 796 | 4855 / 564 |
| model time | 11 s | 7 s |
| repairs segment / content | 0 / 0 | 0 / 0 |

## Per brief

| brief | claude-opus-5-5 checks | claude-sonnet-5-5 checks | repairs s/c | repairs s/c | tokens in/out | tokens in/out | $ | $ | s | s |
|---|---|---|---|---|---|---|---|---|---|---|
| ve-email | ✓ all | ✓ all | 0/0 | 0/0 | 4855/796 | 4855/564 | 0.0353 | 0.0153 | 10.8 | 6.6 |

The first value of each pair is claude-opus-5-5, the second claude-sonnet-5-5. A check name is listed only when it failed.

## Texts and repairs, brief by brief

The SMS and the e-mail subject as the application would have accepted them. Character counts are Unicode characters (limits: SMS 160,
subject 80).

### ve-email

> Ankara'da yaşayan ve en az 5 siparişi olan müşterilerimize sadece e-posta ile teşekkür kampanyası göndermek istiyorum.

- repairs, claude-opus-5-5: none
- repairs, claude-sonnet-5-5: none

| | claude-opus-5-5 | claude-sonnet-5-5 |
|---|---|---|
| SMS | Merhaba {{first_name}}, bizi tekrar tekrar tercih ettiğin için çok teşekkür ederiz. Desteğin bizim için çok değerli. İyi ki varsın! (131) | Merhaba {{first_name}}, bizi tercih ettiğin için çok teşekkür ederiz. Senin gibi değerli müşterilerimiz iyi ki var! (115) |
| e-mail subject | Teşekkürler {{first_name}}, iyi ki bizimlesin! (46) | Bizi tercih ettiğin için teşekkür ederiz, {{first_name}}! (57) |
