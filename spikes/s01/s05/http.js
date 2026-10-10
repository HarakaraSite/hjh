const response = await tools.http(input);
return { status: response.status, bytes: response.body.length, prefix: response.body.slice(0, 100) };
