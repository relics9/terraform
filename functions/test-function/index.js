// 修正前 (Line 42付近)
// const result = someObject.someMethod();

// 修正後
if (someObject == null) {
  console.error('[test-function] Error: someObject is null or undefined. Input:', JSON.stringify(event));
  throw new Error('Required object "someObject" is null. Please verify input data and upstream dependencies.');
}
const result = someObject.someMethod();

// さらに、関数エントリポイントでの入力バリデーション強化
exports.testFunction = (event, context) => {
  // 入力データのnullチェック
  if (!event || !event.data) {
    console.error('[test-function] Invalid input: event or event.data is null');
    return { error: 'Invalid input data' };
  }
  
  try {
    // メイン処理
    const data = event.data;
    // ... 処理 ...
  } catch (error) {
    console.error(`[test-function] Unhandled error: ${error.message}`, { stack: error.stack });
    throw error;
  }
};