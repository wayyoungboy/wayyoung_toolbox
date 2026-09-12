export function settingsPayload(form, passwordChanged) {
  return {
    ...form,
    qb_password: passwordChanged ? form.qb_password : '__UNCHANGED__',
    qb_port: Number(form.qb_port),
    request_timeout: Number(form.request_timeout),
    run_retention_count: Number(form.run_retention_count),
  };
}
