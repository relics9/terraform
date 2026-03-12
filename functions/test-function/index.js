// === 修正前（想定） ===
// const value = someObject.property; // line 42: someObject が null

// === 修正後 ===
// line 42 付近の修正
if (!someObject) {
  console.error('[test-function] someObject is null or undefined.', {
    timestamp: new Date().toISOString(),
    input: JSON.stringify(request?.body || event?.data || 'N/A'),
  });
  // Cloud Function の場合、適切なエラーレスポンスを返す
  return res.status(400).json({
    error: 'Bad Request',
    message: 'Required data is missing or null.',
  });
}

// Optional Chaining を活用した安全なアクセス
const value = someObject?.property ?? defaultValue;

// --- もしJavaの場合 ---
// if (someObject == null) {
//     logger.error("someObject is null at line 42");
//     throw new IllegalArgumentException("Required object is null");
// }
// String value = Optional.ofNullable(someObject.getProperty()).orElse(defaultValue);