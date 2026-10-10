// These definitions are read from disk for every fresh Worker.
let count = 0;

export async function http(args, api) {
  const response = await api.http(args);
  if (response.status < 200 || response.status >= 300) {
    throw new Error(`HTTP status ${response.status}: ${response.body}`);
  }
  return response;
}

export async function command(args, api) {
  const result = await api.command(args);
  if (result.exitCode !== 0) {
    throw new Error(`command exit ${result.exitCode}: ${result.stderr}`);
  }
  return result;
}

export async function pipeline(args, api) {
  const response = await api.tools.http({ url: args.url });
  const values = JSON.parse(response.body).values;
  return api.tools.command({ program: "tr", args: ["a-z", "A-Z"], stdin: values.join("\n") });
}

export async function counter() {
  return ++count;
}

export async function revision() {
  return "revision-one";
}

export async function fail() {
  throw new Error("external tool failure");
}
