# Third-Party Notices

HyperPod's original source code does not currently have a project-level license.
The components and model files used with it have their own licenses and terms.

## Meta Llama 3.2

HyperPod uses Llama 3.2 1B and 3B model files during local inference.

> Llama 3.2 is licensed under the Llama 3.2 Community License,
> Copyright © Meta Platforms, Inc. All Rights Reserved.

- License: https://dev.meta.ai/llama/llama3_2/license
- Acceptable Use Policy: https://dev.meta.ai/llama/llama3_2/use-policy
- Model source: https://huggingface.co/meta-llama

**Built with Llama.**

## Meta Llama 3.1

HyperPod uses a Llama 3.1 8B model file for the pooled-capacity experiment.

> Llama 3.1 is licensed under the Llama 3.1 Community License,
> Copyright © Meta Platforms, Inc. All Rights Reserved.

- License: https://www.llama.com/llama3_1/license/
- Acceptable Use Policy: https://www.llama.com/llama3/use-policy/
- Model source: https://huggingface.co/meta-llama

**Built with Llama.**

## GGUF quantizations

The local tests used GGUF quantizations distributed through Bartowski's
Hugging Face repositories. Those artifacts are not committed to this Git
repository. The underlying Meta model license and acceptable-use terms still
apply.

- Llama 3.2 1B: https://huggingface.co/bartowski/Llama-3.2-1B-Instruct-GGUF
- Llama 3.2 3B: https://huggingface.co/bartowski/Llama-3.2-3B-Instruct-GGUF
- Llama 3.1 8B: https://huggingface.co/bartowski/Meta-Llama-3.1-8B-Instruct-GGUF

## llama.cpp / ggml

HyperPod invokes llama.cpp for model loading and inference and uses its RPC
backend for remote devices. llama.cpp is available under the MIT License.
HyperPod does not vendor or commit the local llama.cpp checkout or binaries.

- Repository and license: https://github.com/ggml-org/llama.cpp
- RPC documentation and security warning:
  https://github.com/ggml-org/llama.cpp/blob/master/tools/rpc/README.md

## Application dependencies

Go, Rust and JavaScript dependencies retain their own licenses. Authoritative
package/version lists are in:

- `worker/Cargo.lock`
- `frontend/package-lock.json`
- `frontend/package.json`

This notice is informational and is not legal advice.
