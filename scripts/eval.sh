#!/bin/sh
# `make eval`: the golden-set evaluation of the real model (eval/golden.json), in its own compose project (scc-eval) so the
# normal stack's data is untouched. It spends money, so it starts only when asked for by name:
#
#   make eval LLM_MODE=anthropic                                   all briefs, budget EVAL_MAX_USD (default 1.00)
#   make eval LLM_MODE=anthropic EVAL_ARGS="-only tek-kriter-sms"  some briefs
#   make eval LLM_MODE=anthropic EVAL_MODEL=claude-sonnet-5-5      another model (the default stays as it is)
#   make eval EVAL_ARGS="-mock"                                    a free dry run of the runner with the demo model
#
# The key is read from the dotenv file by compose and goes to the runner only. Not part of `make test` or of CI.
set -eu
cd "$(dirname "$0")/.."

case " ${EVAL_ARGS:-} " in
*" -mock "*) ;;
*)
	if [ "${LLM_MODE:-}" != "anthropic" ]; then
		echo "make eval calls the real API and costs money: give LLM_MODE=anthropic explicitly (make eval LLM_MODE=anthropic)." >&2
		echo "A free dry run of the runner: make eval EVAL_ARGS=\"-mock\"" >&2
		exit 2
	fi
	;;
esac

compose="docker compose -p scc-eval -f docker-compose.yml -f docker-compose.eval.yml"
cleanup() { $compose down --volumes --remove-orphans >/dev/null 2>&1 || true; }
trap cleanup EXIT INT TERM

# The Go module and build caches the other Go containers use (the compose file declares them external).
docker volume create scc-gomod >/dev/null
docker volume create scc-gocache >/dev/null

$compose run --rm --build eval
